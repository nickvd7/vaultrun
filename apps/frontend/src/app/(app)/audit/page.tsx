"use client";

import { useEffect, useState, useMemo } from "react";
import { RefreshCw, ChevronDown, ChevronRight } from "lucide-react";
import { api } from "@/lib/api";
import { relativeTime, formatDate } from "@/lib/utils";
import type { AuditLog } from "@/types";

// ── action colouring ─────────────────────────────────────────────────────────

type Namespace = "session" | "file" | "command" | "apikey" | "other";

function namespace(action: string): Namespace {
  if (action.startsWith("session.")) return "session";
  if (action.startsWith("file."))    return "file";
  if (action.startsWith("command.")) return "command";
  if (action.startsWith("apikey."))  return "apikey";
  return "other";
}

const NS_CHIP: Record<Namespace, string> = {
  session: "bg-blue-900/30 text-blue-300 border border-blue-800/50",
  file:    "bg-emerald-900/30 text-emerald-300 border border-emerald-800/50",
  command: "bg-indigo-900/30 text-indigo-300 border border-indigo-800/50",
  apikey:  "bg-amber-900/30 text-amber-300 border border-amber-800/50",
  other:   "bg-slate-800 text-slate-400 border border-slate-700",
};

const NS_FILTER_ACTIVE: Record<Namespace | "all", string> = {
  all:     "bg-slate-600 text-slate-100",
  session: "bg-blue-700 text-white",
  file:    "bg-emerald-700 text-white",
  command: "bg-indigo-700 text-white",
  apikey:  "bg-amber-600 text-white",
  other:   "bg-slate-600 text-slate-100",
};

// ── metadata display ─────────────────────────────────────────────────────────

function MetadataPanel({ meta }: { meta: Record<string, unknown> }) {
  const entries = Object.entries(meta);
  if (entries.length === 0) return <span className="text-slate-700">—</span>;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
      {entries.map(([k, v]) => (
        <>
          <dt key={`k-${k}`} className="text-slate-500 whitespace-nowrap">{k}</dt>
          <dd key={`v-${k}`} className="text-slate-300 font-mono break-all">
            {typeof v === "string" ? v : JSON.stringify(v)}
          </dd>
        </>
      ))}
    </dl>
  );
}

// ── page ──────────────────────────────────────────────────────────────────────

type NSFilter = Namespace | "all";

export default function AuditPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [nsFilter, setNsFilter] = useState<NSFilter>("all");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const load = () => {
    setLoading(true);
    api.audit
      .list()
      .then(setLogs)
      .catch(console.error)
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const filtered = useMemo(() => {
    return logs.filter((l) => {
      if (nsFilter !== "all" && namespace(l.action) !== nsFilter) return false;
      if (!search) return true;
      const q = search.toLowerCase();
      return (
        l.action.includes(q) ||
        l.actor.toLowerCase().includes(q) ||
        (l.session_id?.includes(q) ?? false) ||
        (l.run_id?.includes(q) ?? false)
      );
    });
  }, [logs, search, nsFilter]);

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  const namespaces: NSFilter[] = ["all", "session", "file", "command", "apikey"];

  return (
    <div className="max-w-5xl mx-auto space-y-5">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Audit Logs</h1>
          <p className="text-slate-500 text-sm mt-1">
            Immutable record of all security-relevant events
          </p>
        </div>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-400 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-50"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {/* Filters */}
      <div className="flex flex-col sm:flex-row gap-3">
        <input
          className="flex-1 bg-[#0f0f1a] border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-600"
          placeholder="Search actor, action, session ID…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <div className="flex gap-1.5 flex-wrap">
          {namespaces.map((ns) => {
            const active = nsFilter === ns;
            return (
              <button
                key={ns}
                onClick={() => setNsFilter(ns)}
                className={`px-3 py-1.5 rounded text-xs font-medium transition-colors capitalize ${
                  active
                    ? NS_FILTER_ACTIVE[ns]
                    : "bg-slate-800 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
                }`}
              >
                {ns}
              </button>
            );
          })}
        </div>
      </div>

      {/* Count */}
      {!loading && (
        <p className="text-xs text-slate-600">
          {filtered.length} {filtered.length === 1 ? "event" : "events"}
          {filtered.length !== logs.length && ` of ${logs.length}`}
        </p>
      )}

      {/* Table */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-600 text-sm">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-8 text-center text-slate-600 text-sm">No events match your filters.</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="w-6 px-3 py-3" />
                <th className="px-4 py-3 text-left">When</th>
                <th className="px-4 py-3 text-left">Actor</th>
                <th className="px-4 py-3 text-left">Action</th>
                <th className="px-4 py-3 text-left">Session</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((l) => {
                const open = expanded.has(l.id);
                const hasDetail = Object.keys(l.metadata).length > 0 || l.run_id;
                return (
                  <>
                    <tr
                      key={l.id}
                      onClick={() => hasDetail && toggle(l.id)}
                      className={`border-t border-slate-800/50 transition-colors ${
                        hasDetail ? "cursor-pointer hover:bg-slate-800/30" : "hover:bg-slate-800/10"
                      }`}
                    >
                      <td className="px-3 py-2.5 text-slate-600">
                        {hasDetail ? (
                          open
                            ? <ChevronDown className="w-3.5 h-3.5" />
                            : <ChevronRight className="w-3.5 h-3.5" />
                        ) : null}
                      </td>
                      <td className="px-4 py-2.5 text-xs text-slate-400 whitespace-nowrap">
                        <span title={formatDate(l.timestamp)}>
                          {relativeTime(l.timestamp)}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-slate-400 font-mono truncate max-w-[120px]"
                          title={l.actor}>
                        {l.actor.length > 16 ? l.actor.slice(0, 16) + "…" : l.actor}
                      </td>
                      <td className="px-4 py-2.5">
                        <span className={`font-mono text-xs px-2 py-0.5 rounded ${NS_CHIP[namespace(l.action)]}`}>
                          {l.action}
                        </span>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-xs text-slate-600">
                        {l.session_id ? l.session_id.slice(0, 8) + "…" : "—"}
                      </td>
                    </tr>

                    {open && (
                      <tr key={`${l.id}-detail`} className="bg-slate-900/40 border-t border-slate-800/30">
                        <td />
                        <td colSpan={4} className="px-4 py-3 text-xs">
                          <div className="space-y-2">
                            {l.run_id && (
                              <p className="text-slate-500">
                                run: <span className="font-mono text-slate-400">{l.run_id}</span>
                              </p>
                            )}
                            <MetadataPanel meta={l.metadata} />
                          </div>
                        </td>
                      </tr>
                    )}
                  </>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
