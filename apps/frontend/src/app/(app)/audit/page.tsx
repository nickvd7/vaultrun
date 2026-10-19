"use client";

import Link from "next/link";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { RefreshCw, ChevronDown, ChevronRight, ChevronLeft, Download, ShieldCheck, ShieldQuestion } from "lucide-react";
import { api } from "@/lib/api";
import { useApiKey } from "@/components/ApiKeyGate";
import { Notice } from "@/components/ui/Notice";
import { useToast } from "@/components/ui/Toast";
import { badgeBase } from "@/lib/utils";
import { Time } from "@/components/ui/Time";
import { OutOfOrderChip, SigCell, MetadataPanel, NS_CHIP, actorLabel, namespace, sigState } from "@/components/audit/shared";
import type { Namespace } from "@/components/audit/shared";
import type { AuditIntegrity, AuditIntegrityEntry, AuditLog } from "@/types";

// ── action colouring ─────────────────────────────────────────────────────────

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
const NS_LABEL: Record<NSFilter, string> = { all: "All", session: "Session", file: "File", command: "Command", apikey: "API key", secret: "Secret" };
const PAGE_SIZE = 50;
const EXPORT_CAP = 5000;

// ── metadata display ─────────────────────────────────────────────────────────

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
  const header = ["seq", "timestamp", "actor", "actor_name", "action", "session_id", "run_id", "sig_status", "sig_kind", "sig_reason", "out_of_order", "metadata"];
  const lines = rows.map((l) =>
    [l.seq, l.timestamp, l.actor, l.actor_name, l.action, l.session_id, l.run_id, sigState(l)?.kind, l.sig_kind, l.sig_reason, l.out_of_order, l.metadata].map(csvCell).join(",")
  );
  return [header.join(","), ...lines].join("\n") + "\n";
}

/** Verdict over the whole log, so a problem on page 2 is visible from page 1. */
const KIND_LABEL: Record<AuditIntegrityEntry["kind"], string> = { altered: "Altered", chain: "Affected" };

function IntegrityBanner({
  integrity,
  isMaster,
  onShow,
}: {
  integrity: AuditIntegrity;
  isMaster: boolean | null;
  onShow: (e: AuditIntegrityEntry) => void;
}) {
  if (integrity.status === "compromised") {
    const own = integrity.scope === "own";
    const altered = integrity.altered_entries;
    const affected = integrity.affected_entries;
    const entries = integrity.entries ?? [];
    const shownTotal = own ? integrity.own_invalid_entries ?? entries.length : integrity.invalid_entries;
    return (
      <Notice tone="error" title="The audit log failed verification">
        {own ? (
          <ul className="list-disc pl-5 space-y-0.5">
            {integrity.problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        ) : (
          <>
            <ul className="list-disc pl-5 space-y-0.5">
              {altered !== undefined && affected !== undefined ? (
                <>
                  {altered > 0 && (
                    <li>
                      <strong>{altered} altered</strong>: their content or signature was changed, or the signature was removed.
                    </li>
                  )}
                  {affected > 0 && (
                    <li>
                      <strong>{affected} affected</strong>: intact themselves, but they follow an altered entry, so the chain breaks at them. These are
                      consequences, not extra tampering.
                    </li>
                  )}
                  {integrity.missing_entries > 0 && (
                    <li>
                      <strong>{integrity.missing_entries} missing</strong>
                      {integrity.missing_seqs && integrity.missing_seqs.length > 0 && (
                        <>
                          {" "}
                          (chain numbers <span className="font-mono">{integrity.missing_seqs.join(", ")}</span>)
                        </>
                      )}
                      : deleted from the log.
                    </li>
                  )}
                </>
              ) : (
                integrity.problems.map((p) => <li key={p}>{p}</li>)
              )}
            </ul>
            {altered === undefined && integrity.missing_seqs && integrity.missing_seqs.length > 0 && (
              <p className="mt-2">
                Missing sequence numbers: <span className="font-mono">{integrity.missing_seqs.join(", ")}</span>
              </p>
            )}
          </>
        )}
        {entries.length > 0 && (
          <>
            <p className="mt-3 font-medium">{own ? "Your entries that failed:" : "Entries that failed:"}</p>
            <ul className="mt-1 space-y-1">
              {entries.map((e) => (
                <li key={e.id} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <span className="font-mono">{e.seq ? `#${e.seq}` : "no number"}</span>
                  <span className={e.kind === "chain" ? "text-amber-200" : "text-red-200"}>{KIND_LABEL[e.kind]}</span>
                  <span>{e.reason}{e.out_of_order && ", dated out of order"}</span>
                  <button type="button" onClick={() => onShow(e)} className="text-indigo-200 underline hover:text-indigo-100">
                    Show in table<span className="sr-only"> entry {e.seq ? `#${e.seq}` : e.id}</span>
                  </button>
                </li>
              ))}
            </ul>
            {shownTotal > entries.length && <p className="mt-1">…and {shownTotal - entries.length} more.</p>}
          </>
        )}
        <p className="mt-2">
          {own
            ? "Entries recorded under other keys are not visible to you, which is why the numbers above can exceed what the table shows. "
            : ""}
          Entries may have been altered or deleted by someone with write access to the database. Entries that were changed are marked
          Altered and the entry after a change is marked Affected, in the table and on every page of the log; open a row for the full reason. The API logs this too.
        </p>
      </Notice>
    );
  }
  if (integrity.status === "ok" && isMaster !== true) {
    return (
      <p className="flex items-center gap-1.5 text-xs text-emerald-300" role="status">
        <ShieldCheck className="w-3.5 h-3.5" aria-hidden="true" />
        Log integrity check passed. You only see your own entries here; the check covers the whole log, which only the master key can inspect
        {" · "}checked <Time value={integrity.checked_at} />
      </p>
    );
  }
  if (integrity.status === "ok") {
    return (
      <p className="flex items-center gap-1.5 text-xs text-emerald-300" role="status">
        <ShieldCheck className="w-3.5 h-3.5" aria-hidden="true" />
        Whole log verified: {integrity.chained_entries} chained {integrity.chained_entries === 1 ? "entry" : "entries"}, none altered or missing
        {integrity.legacy_entries > 0 && ` (${integrity.legacy_entries} older ${integrity.legacy_entries === 1 ? "entry" : "entries"} from before chaining cannot be chain-checked)`}
        {" · "}checked <Time value={integrity.checked_at} />
      </p>
    );
  }
  if (integrity.status === "disabled") {
    return (
      <p className="flex items-center gap-1.5 text-xs text-amber-300" role="status">
        <ShieldQuestion className="w-3.5 h-3.5" aria-hidden="true" />
        Audit signing is off (AUDIT_HMAC_KEY is not set), so entries cannot be verified.
      </p>
    );
  }
  return null;
}

// ── page ──────────────────────────────────────────────────────────────────────

type SortOrder = "time" | "seq";
type Focus = { seq?: number; id?: string; label: string };

export default function AuditPage() {
  const { isMaster } = useApiKey();
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
  const [integrity, setIntegrity] = useState<AuditIntegrity | null>(null);
  const [sort, setSort] = useState<SortOrder>("time");
  const [focus, setFocus] = useState<Focus | null>(null);
  const pendingFocusId = useRef<string | null>(null);

  const loadIntegrity = useCallback((refresh: boolean) => {
    api.audit
      .integrity(refresh)
      .then(setIntegrity)
      .catch(() => setIntegrity(null));
  }, []);

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
      sort,
      seq: focus?.seq,
      id: focus?.seq ? undefined : focus?.id,
    }),
    [nsFilter, apiFrom, apiTo, sort, focus]
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
  useEffect(() => loadIntegrity(false), [loadIntegrity]);
  useEffect(() => setPage(1), [query]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return logs;
    return logs.filter(
      (l) =>
        l.action.toLowerCase().includes(q) ||
        l.actor.toLowerCase().includes(q) ||
        (l.actor_name ?? "").toLowerCase().includes(q) ||
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

  const showEntry = (e: AuditIntegrityEntry) => {
    setNsFilter("all");
    setFrom("");
    setTo("");
    setSearch("");
    setFocus(e.seq ? { seq: e.seq, label: `entry #${e.seq}` } : { id: e.id, label: "one entry" });
    setExpanded(new Set([e.id]));
    pendingFocusId.current = e.id;
  };

  // Once the filtered list has loaded, move keyboard focus (and the screen
  // reader cursor) to the entry that "Show in table" asked for.
  useEffect(() => {
    const id = pendingFocusId.current;
    if (!id || loading) return;
    const row = document.getElementById(`audit-row-${id}`);
    if (row) {
      pendingFocusId.current = null;
      row.focus();
      row.scrollIntoView?.({ block: "center" });
    }
  }, [logs, loading, filtered]);

  const hasFilters = nsFilter !== "all" || from !== "" || to !== "" || focus !== null;
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
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-60"
          >
            <Download className="w-3.5 h-3.5" aria-hidden="true" />
            {exporting ? "Exporting…" : "Export CSV"}
          </button>
          <button
            onClick={() => exportAll("json")}
            disabled={exporting || total === 0}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-60"
          >
            <Download className="w-3.5 h-3.5" aria-hidden="true" />
            Export JSON
          </button>
          <button
            onClick={() => {
              load();
              loadIntegrity(true);
            }}
            disabled={loading}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-60"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
            Refresh
          </button>
        </div>
      </div>

      {error && <Notice tone="error" title="Could not load the audit log">{error}</Notice>}

      {integrity && <IntegrityBanner integrity={integrity} isMaster={isMaster} onShow={showEntry} />}

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
          <div className="flex items-center gap-2 text-xs text-slate-200">
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
                className={`px-3 py-1.5 rounded text-xs font-medium transition-colors ${
                  active ? NS_FILTER_ACTIVE[ns] : "bg-slate-800 text-slate-300 hover:bg-slate-700 hover:text-slate-100"
                }`}
              >
                {NS_LABEL[ns]}
              </button>
            );
          })}
          {hasFilters && (
            <button
              onClick={() => {
                setNsFilter("all");
                setFrom("");
                setTo("");
                setFocus(null);
              }}
              className="ml-2 text-xs text-indigo-200 hover:underline"
            >
              Clear filters
            </button>
          )}
        </div>
        <div className="flex items-center gap-3 flex-wrap text-xs text-slate-200">
          <div role="group" aria-label="Sort order" className="flex items-center gap-1.5">
            <span>Order:</span>
            {([["time", "Newest first"], ["seq", "Chain order (#)"]] as [SortOrder, string][]).map(([v, label]) => (
              <button
                key={v}
                type="button"
                onClick={() => setSort(v)}
                aria-pressed={sort === v}
                title={v === "seq" ? "Order by chain number, so an entry with a changed time stays between its neighbours" : undefined}
                className={`px-3 py-1.5 rounded text-xs font-medium transition-colors ${
                  sort === v ? "bg-slate-600 text-slate-100" : "bg-slate-800 text-slate-300 hover:bg-slate-700 hover:text-slate-100"
                }`}
              >
                {label}
              </button>
            ))}
          </div>
          {focus && (
            <span role="status" className="inline-flex items-center gap-2 rounded bg-indigo-900/40 border border-indigo-800/50 px-2.5 py-1 text-indigo-100">
              Showing only {focus.label}
              <button type="button" onClick={() => setFocus(null)} className="underline hover:text-white">
                Show all
              </button>
            </span>
          )}
        </div>
      </div>

      {!integrity && !loading && !error && logs.some((l) => l.sig_status === "invalid") && (
        <Notice tone="error" title="Some entries failed signature verification">
          {logs.filter((l) => l.sig_status === "invalid").length} entr
          {logs.filter((l) => l.sig_status === "invalid").length === 1 ? "y" : "ies"} on this page do not match their
          signature, or lost it, since audit signing began. They are marked Altered or Affected and may have been changed.
        </Notice>
      )}

      {!loading && !error && (
        <p className="text-xs text-slate-200" aria-live="polite">
          {total === 0
            ? "0 events"
            : `Events ${firstShown}–${lastShown} of ${total}`}
          {search.trim() && ` · ${filtered.length} match${filtered.length === 1 ? "" : "es"} on this page`}
        </p>
      )}

      <div id="audit-table" className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-300 text-sm" role="status">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-8 text-center text-slate-300 text-sm">
            {total === 0 && !hasFilters ? "No audit events yet." : "No events match your filters."}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
                <th scope="col" className="w-8 px-3 py-3">
                  <span className="sr-only">Details</span>
                </th>
                <th scope="col" className="px-3 py-3 text-left font-medium" title="Position in the hash chain">
                  <span aria-hidden="true">#</span>
                  <span className="sr-only">Chain number</span>
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
                const hasDetail = true;
                return (
                  <Fragment key={l.id}>
                    <tr id={`audit-row-${l.id}`} tabIndex={-1} className={`border-t border-slate-800/50 transition-colors hover:bg-slate-800/20 focus:outline focus:outline-2 focus:-outline-offset-2 focus:outline-indigo-400 ${l.sig_status === "invalid" ? (l.sig_kind === "chain" ? "bg-amber-950/20" : "bg-red-950/20") : ""}`}>
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
                      <td className="px-3 py-2.5 text-xs text-slate-200 font-mono align-top">
                        {l.seq ?? <span className="text-slate-300" title="Written before chaining began">—</span>}
                      </td>
                      <td className="px-4 py-2.5 text-xs text-slate-200 whitespace-nowrap">
                        <Time value={l.timestamp} />
                        {l.out_of_order && (
                          <div>
                            <OutOfOrderChip prevSeq={l.seq ? l.seq - 1 : undefined} />
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-2.5 text-xs text-slate-200 font-mono truncate max-w-[140px]" title={l.actor_name ? `${l.actor_name} (${l.actor})` : l.actor}>
                        {actorLabel(l)}
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
                        <td colSpan={6} className="px-4 py-3 text-xs">
                          <div className="space-y-2">
                            {(l.sig_status === "invalid" || l.out_of_order) && (
                              <div
                                className={`rounded border px-3 py-2 ${
                                  l.sig_kind === "chain" ? "border-amber-900/60 bg-amber-950/20" : "border-red-900/60 bg-red-950/20"
                                }`}
                              >
                                <p className={`font-medium ${l.sig_kind === "chain" ? "text-amber-200" : "text-red-200"}`}>
                                  {l.sig_status !== "invalid"
                                    ? "Dated out of order"
                                    : l.sig_kind === "chain"
                                      ? "Affected: the chain breaks here"
                                      : "Altered: failed verification"}
                                  {l.sig_reason && <span className="font-normal"> ({l.sig_reason})</span>}
                                </p>
                                <p className="mt-1 text-slate-100">{l.sig_detail || "The signature of this entry does not verify."}</p>
                                <p className="mt-1 text-slate-200">
                                  Entry ID <span className="font-mono">{l.id}</span>
                                  {l.seq ? <> · chain number <span className="font-mono">#{l.seq}</span></> : null}
                                </p>
                                {isMaster && l.seq && l.seq > 1 && (
                                  <button
                                    type="button"
                                    onClick={() => setFocus({ seq: l.seq! - 1, label: `entry #${l.seq! - 1}` })}
                                    className="mt-1 text-indigo-200 underline hover:text-indigo-100"
                                  >
                                    Show entry #{l.seq - 1} before it
                                  </button>
                                )}
                              </div>
                            )}
                            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
                              <dt className="text-slate-300">entry</dt>
                              <dd className="text-slate-200 font-mono break-all">{l.id}{l.seq ? ` (#${l.seq})` : ""}</dd>
                              <dt className="text-slate-300">actor</dt>
                              <dd className="text-slate-200 font-mono break-all">{l.actor_name ? `${l.actor_name} (${l.actor})` : l.actor}</dd>
                              {l.session_id && (
                                <>
                                  <dt className="text-slate-300">session</dt>
                                  <dd className="text-slate-200 font-mono break-all">{l.session_id}</dd>
                                </>
                              )}
                              {l.run_id && (
                                <>
                                  <dt className="text-slate-300">run</dt>
                                  <dd className="text-slate-200 font-mono break-all">{l.run_id}</dd>
                                </>
                              )}
                            </dl>
                            {Object.keys(l.metadata).length > 0 ? (
                              <MetadataPanel meta={l.metadata} />
                            ) : (
                              <p className="text-slate-300">No additional details were recorded for this event.</p>
                            )}
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
        <nav aria-label="Audit log pages" className="flex items-center justify-between text-xs text-slate-200">
          <span>
            Page {page} of {pageCount}
          </span>
          <div className="flex gap-2">
            <button
              onClick={() => setPage(page - 1)}
              disabled={page <= 1 || loading}
              className="flex items-center gap-1 px-3 py-1.5 rounded border border-slate-700 hover:bg-slate-800 disabled:opacity-60"
            >
              <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" /> Newer
            </button>
            <button
              onClick={() => setPage(page + 1)}
              disabled={page >= pageCount || loading}
              className="flex items-center gap-1 px-3 py-1.5 rounded border border-slate-700 hover:bg-slate-800 disabled:opacity-60"
            >
              Older <ChevronRight className="w-3.5 h-3.5" aria-hidden="true" />
            </button>
          </div>
        </nav>
      )}
    </div>
  );
}
