"use client";

import Link from "next/link";
import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { RefreshCw, ChevronDown, ChevronRight, ChevronLeft, Download, ShieldCheck, ShieldAlert, ShieldQuestion } from "lucide-react";
import { api } from "@/lib/api";
import { Notice } from "@/components/ui/Notice";
import { useToast } from "@/components/ui/Toast";
import { badgeBase } from "@/lib/utils";
import { Time } from "@/components/ui/Time";
import type { AuditLog } from "@/types";

// ── action colouring ─────────────────────────────────────────────────────────

type Namespace = "session" | "file" | "command" | "apikey" | "secret" | "other";

function namespace(action: string): Namespace {
  if (action.startsWith("session.")) return "session";
  if (action.startsWith("file.")) return "file";
  if (action.startsWith("command.")) return "command";
  if (action.startsWith("apikey.")) return "apikey";
  if (action.startsWith("secret.")) return "secret";
  return "other";
}

const NS_CHIP: Record<Namespace, string> = {
  session: "bg-blue-900/30 text-blue-200 border border-blue-800/50",
  file: "bg-emerald-900/30 text-emerald-200 border border-emerald-800/50",
  command: "bg-indigo-900/30 text-indigo-200 border border-indigo-800/50",
  apikey: "bg-amber-900/30 text-amber-200 border border-amber-800/50",
  secret: "bg-rose-900/30 text-rose-200 border border-rose-800/50",
  other: "bg-slate-800 text-slate-300 border border-slate-700",
};

type NSFilter = Exclude<Namespace, "other"> | "all";

const NS_FILTER_ACTIVE: Record<NSFilter, string> = {
  all: "bg-slate-600 text-slate-100",
  session: "bg-blue-700 text-white",
  file: "bg-emerald-700 text-white",
  command: "bg-indigo-700 text-white",
  apikey: "bg-amber-600 text-white",
  secret: "bg-rose-700 text-white",
};

const NAMESPACES: NSFilter[] = ["all", "session", "file", "command", "apikey", "secret"];
const PAGE_SIZE = 50;
const EXPORT_CAP = 5000;

// ── metadata display ─────────────────────────────────────────────────────────

function MetadataPanel({ meta }: { meta: Record<string, unknown> }) {
  const entries = Object.entries(meta);
  if (entries.length === 0) return <span className="text-slate-300">—</span>;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
      {entries.map(([k, v]) => (
        <Fragment key={k}>
          <dt className="text-slate-300 whitespace-nowrap">{k}</dt>
          <dd className="text-slate-200 font-mono break-all">{typeof v === "string" ? v : JSON.stringify(v)}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

function download(filename: string, mime: string, content: string) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

function csvCell(v: unknown): string {
  const s = v === undefined || v === null ? "" : typeof v === "string" ? v : JSON.stringify(v);
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

function toCSV(rows: AuditLog[]): string {
  const header = ["timestamp", "actor", "action", "session_id", "run_id", "sig_status", "metadata"];
  const lines = rows.map((l) =>
    [l.timestamp, l.actor, l.action, l.session_id, l.run_id, sigState(l)?.kind, l.metadata].map(csvCell).join(",")
  );
  return [header.join(","), ...lines].join("\n") + "\n";
}

type SigKind = "valid" | "invalid" | "unsigned" | "unverifiable" | "unverified";

interface SigState {
  kind: SigKind;
  label: string;
  title: string;
  className: string;
}

const SIG_STATES: Record<SigKind, Omit<SigState, "kind">> = {
  valid: { label: "Verified", title: "Signature verified", className: "text-emerald-400" },
  invalid: {
    label: "Invalid",
    title: "Signature does not match, or was removed: this entry may have been altered",
    className: "text-red-400",
  },
  unsigned: {
    label: "Unsigned",
    title: "Written before audit signing was enabled",
    className: "text-slate-300",
  },
  unverifiable: {
    label: "Legacy",
    title: "Signed by an older version whose signatures cannot be verified",
    className: "text-slate-300",
  },
  unverified: {
    label: "Unverified",
    title: "Signature does not verify (this API does not say whether the entry predates signing)",
    className: "text-amber-400",
  },
};

/** What to show for an entry's signature; null when the server does not sign entries. */
function sigState(l: AuditLog): SigState | null {
  const kind: SigKind | null = l.sig_status ?? (l.sig_valid === true ? "valid" : l.sig_valid === false ? "unverified" : null);
  return kind ? { kind, ...SIG_STATES[kind] } : null;
}

function SigCell({ log }: { log: AuditLog }) {
  const st = sigState(log);
  if (!st) return null;
  const Icon = st.kind === "valid" ? ShieldCheck : st.kind === "invalid" || st.kind === "unverified" ? ShieldAlert : ShieldQuestion;
  return (
    <span title={st.title} className={`inline-flex items-center gap-1 text-xs ${st.className}`}>
      <Icon className="w-3.5 h-3.5" aria-hidden="true" />
      {st.label}
    </span>
  );
}

// ── page ──────────────────────────────────────────────────────────────────────

export default function AuditPage() {
  const toast = useToast();
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [nsFilter, setNsFilter] = useState<NSFilter>("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [exporting, setExporting] = useState(false);

  // The date inputs are calendar days in the viewer's time zone. Send them as
  // instants (local midnight), not bare dates, which the API reads as UTC.
  // "to" is inclusive in the UI and exclusive in the API, so it is the start of
  // the following day.
  const apiFrom = useMemo(() => (from ? new Date(`${from}T00:00:00`).toISOString() : undefined), [from]);
  const apiTo = useMemo(() => {
    if (!to) return undefined;
    const d = new Date(`${to}T00:00:00`);
    d.setDate(d.getDate() + 1);
    return d.toISOString();
  }, [to]);

  const query = useMemo(
    () => ({
      action: nsFilter === "all" ? undefined : `${nsFilter}.`,
      from: apiFrom,
      to: apiTo,
    }),
    [nsFilter, apiFrom, apiTo]
  );

  const load = useCallback(() => {
    setLoading(true);
    setError("");
    api.audit
      .list({ ...query, page, limit: PAGE_SIZE })
      .then((r) => {
        setLogs(r.logs);
        setTotal(r.total);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "Failed to load audit log"))
      .finally(() => setLoading(false));
  }, [query, page]);

  useEffect(load, [load]);
  useEffect(() => setPage(1), [query]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return logs;
    return logs.filter(
      (l) =>
        l.action.toLowerCase().includes(q) ||
        l.actor.toLowerCase().includes(q) ||
        (l.session_id?.toLowerCase().includes(q) ?? false) ||
        (l.run_id?.toLowerCase().includes(q) ?? false)
    );
  }, [logs, search]);

  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const exportAll = async (format: "csv" | "json") => {
    setExporting(true);
    try {
      const rows: AuditLog[] = [];
      for (let p = 1; rows.length < EXPORT_CAP; p++) {
        const r = await api.audit.list({ ...query, page: p, limit: 200 });
        rows.push(...r.logs);
        if (r.logs.length < 200 || rows.length >= r.total) break;
      }
      const capped = rows.slice(0, EXPORT_CAP);
      const stamp = new Date().toISOString().slice(0, 10);
      if (format === "csv") download(`vaultrun-audit-${stamp}.csv`, "text/csv", toCSV(capped));
      else download(`vaultrun-audit-${stamp}.json`, "application/json", JSON.stringify(capped, null, 2));
      toast.success(`Exported ${capped.length} event${capped.length === 1 ? "" : "s"}${rows.length >= EXPORT_CAP ? ` (capped at ${EXPORT_CAP})` : ""}`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Export failed");
    } finally {
      setExporting(false);
    }
  };

  const hasFilters = nsFilter !== "all" || from !== "" || to !== "";
  const firstShown = total === 0 ? 0 : (page - 1) * PAGE_SIZE + 1;
  const lastShown = (page - 1) * PAGE_SIZE + logs.length;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Audit Logs</h1>
          <p className="text-slate-300 text-sm mt-1">Record of all security-relevant events</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => exportAll("csv")}
            disabled={exporting || total === 0}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-50"
          >
            <Download className="w-3.5 h-3.5" aria-hidden="true" />
            {exporting ? "Exporting…" : "Export CSV"}
          </button>
          <button
            onClick={() => exportAll("json")}
            disabled={exporting || total === 0}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-50"
          >
            <Download className="w-3.5 h-3.5" aria-hidden="true" />
            Export JSON
          </button>
          <button
            onClick={load}
            disabled={loading}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-50"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
            Refresh
          </button>
        </div>
      </div>

      {error && <Notice tone="error" title="Could not load the audit log">{error}</Notice>}

      <div className="flex flex-col gap-3">
        <div className="flex flex-col sm:flex-row gap-3">
          <input
            type="search"
            aria-label="Search this page by actor, action, session or run ID"
            className="flex-1 bg-[#0f0f1a] border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-400"
            placeholder="Search this page: actor, action, session ID…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <div className="flex items-center gap-2 text-xs text-slate-300">
            <label htmlFor="audit-from">From</label>
            <input
              id="audit-from"
              type="date"
              value={from}
              max={to || undefined}
              onChange={(e) => setFrom(e.target.value)}
              className="bg-[#0f0f1a] border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
            />
            <label htmlFor="audit-to">to</label>
            <input
              id="audit-to"
              type="date"
              value={to}
              min={from || undefined}
              onChange={(e) => setTo(e.target.value)}
              className="bg-[#0f0f1a] border border-slate-700 rounded px-2 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
            />
          </div>
        </div>
        <div className="flex gap-1.5 flex-wrap items-center" role="group" aria-label="Filter by event type">
          {NAMESPACES.map((ns) => {
            const active = nsFilter === ns;
            return (
              <button
                key={ns}
                onClick={() => setNsFilter(ns)}
                aria-pressed={active}
                className={`px-3 py-1.5 rounded text-xs font-medium transition-colors capitalize ${
                  active ? NS_FILTER_ACTIVE[ns] : "bg-slate-800 text-slate-300 hover:bg-slate-700 hover:text-slate-100"
                }`}
              >
                {ns}
              </button>
            );
          })}
          {hasFilters && (
            <button
              onClick={() => {
                setNsFilter("all");
                setFrom("");
                setTo("");
              }}
              className="ml-2 text-xs text-indigo-300 hover:underline"
            >
              Clear filters
            </button>
          )}
        </div>
      </div>

      {!loading && !error && logs.some((l) => l.sig_status === "invalid") && (
        <Notice tone="error" title="Some entries failed signature verification">
          {logs.filter((l) => l.sig_status === "invalid").length} entr
          {logs.filter((l) => l.sig_status === "invalid").length === 1 ? "y" : "ies"} on this page do not match their
          signature, or lost it, since audit signing began. They may have been altered.
        </Notice>
      )}

      {!loading && !error && (
        <p className="text-xs text-slate-300" aria-live="polite">
          {total === 0
            ? "0 events"
            : `Events ${firstShown}–${lastShown} of ${total}`}
          {search.trim() && ` · ${filtered.length} match${filtered.length === 1 ? "" : "es"} on this page`}
        </p>
      )}

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-300 text-sm" role="status">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-8 text-center text-slate-300 text-sm">
            {total === 0 && !hasFilters ? "No audit events yet." : "No events match your filters."}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-300 uppercase tracking-wide">
                <th scope="col" className="w-8 px-3 py-3">
                  <span className="sr-only">Details</span>
                </th>
                <th scope="col" className="px-4 py-3 text-left font-medium">When</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Actor</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Action</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Session</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">
                  Signature
                </th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((l) => {
                const open = expanded.has(l.id);
                const hasDetail = Object.keys(l.metadata).length > 0 || !!l.run_id;
                return (
                  <Fragment key={l.id}>
                    <tr className="border-t border-slate-800/50 transition-colors hover:bg-slate-800/20">
                      <td className="px-3 py-2.5 text-slate-300">
                        {hasDetail && (
                          <button
                            type="button"
                            onClick={() => toggle(l.id)}
                            aria-expanded={open}
                            aria-label={`${open ? "Hide" : "Show"} details for ${l.action}`}
                            className="rounded p-0.5 hover:bg-slate-700"
                          >
                            {open ? <ChevronDown className="w-3.5 h-3.5" aria-hidden="true" /> : <ChevronRight className="w-3.5 h-3.5" aria-hidden="true" />}
                          </button>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-xs text-slate-300 whitespace-nowrap">
                        <Time value={l.timestamp} />
                      </td>
                      <td className="px-4 py-2.5 text-xs text-slate-300 font-mono truncate max-w-[120px]" title={l.actor}>
                        {l.actor.length > 16 ? l.actor.slice(0, 16) + "…" : l.actor}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className={`${badgeBase} ${NS_CHIP[namespace(l.action)]}`}>{l.action}</span>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-xs">
                        {l.session_id ? (
                          <Link href={`/sessions/${l.session_id}`} className="text-indigo-300 hover:underline" title={l.session_id}>
                            {l.session_id.slice(0, 8)}…
                          </Link>
                        ) : (
                          <span className="text-slate-300">—</span>
                        )}
                      </td>
                      <td className="px-4 py-2.5">
                        <SigCell log={l} />
                      </td>
                    </tr>

                    {open && (
                      <tr className="bg-slate-900/40 border-t border-slate-800/30">
                        <td />
                        <td colSpan={5} className="px-4 py-3 text-xs">
                          <div className="space-y-2">
                            {l.run_id && (
                              <p className="text-slate-300">
                                run: <span className="font-mono text-slate-200">{l.run_id}</span>
                              </p>
                            )}
                            <MetadataPanel meta={l.metadata} />
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {total > PAGE_SIZE && (
        <nav aria-label="Audit log pages" className="flex items-center justify-between text-xs text-slate-300">
          <span>
            Page {page} of {pageCount}
          </span>
          <div className="flex gap-2">
            <button
              onClick={() => setPage(page - 1)}
              disabled={page <= 1 || loading}
              className="flex items-center gap-1 px-3 py-1.5 rounded border border-slate-700 hover:bg-slate-800 disabled:opacity-40"
            >
              <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" /> Newer
            </button>
            <button
              onClick={() => setPage(page + 1)}
              disabled={page >= pageCount || loading}
              className="flex items-center gap-1 px-3 py-1.5 rounded border border-slate-700 hover:bg-slate-800 disabled:opacity-40"
            >
              Older <ChevronRight className="w-3.5 h-3.5" aria-hidden="true" />
            </button>
          </div>
        </nav>
      )}
    </div>
  );
}
