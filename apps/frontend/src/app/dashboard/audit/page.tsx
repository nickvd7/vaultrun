"use client";

import { useEffect, useState } from "react";
import { RefreshCw, AlertCircle, Loader2, ScrollText } from "lucide-react";

const API = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

async function apiFetch(path: string) {
  const res = await fetch(`${API}${path}`, {
    headers: { "X-API-Key": API_KEY, "Content-Type": "application/json" },
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error ?? res.statusText);
  }
  return res.json();
}

type AuditLog = {
  id: string;
  actor: string;
  action: string;
  timestamp: string;
  session_id?: string;
  run_id?: string;
  metadata?: Record<string, unknown>;
};

const ACTION_COLORS: Record<string, string> = {
  session_create: "text-green-400 bg-green-500/10 border-green-500/20",
  session_delete: "text-red-400 bg-red-500/10 border-red-500/20",
  run_create: "text-indigo-400 bg-indigo-500/10 border-indigo-500/20",
  file_upload: "text-yellow-400 bg-yellow-500/10 border-yellow-500/20",
  file_download: "text-sky-400 bg-sky-500/10 border-sky-500/20",
  key_create: "text-purple-400 bg-purple-500/10 border-purple-500/20",
};

function actionColor(action: string) {
  return ACTION_COLORS[action] ?? "text-slate-400 bg-slate-700/30 border-slate-600/30";
}

export default function AuditPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError("");
    try {
      const data = await apiFetch("/api/v1/audit?limit=100");
      setLogs(data.audit_logs ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  const filtered = filter
    ? logs.filter(l =>
        l.action.includes(filter) ||
        l.actor.includes(filter) ||
        (l.session_id ?? "").includes(filter)
      )
    : logs;

  return (
    <div className="p-8">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-xl font-semibold text-slate-100">Audit Logs</h1>
          <p className="text-slate-500 text-sm mt-0.5">HMAC-signed event trail</p>
        </div>
        <button
          onClick={load}
          className="flex items-center gap-1.5 px-3 py-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 hover:text-slate-200 text-sm transition-colors"
        >
          <RefreshCw className="w-3.5 h-3.5" />
          Refresh
        </button>
      </div>

      {/* Search */}
      <div className="mb-6">
        <input
          value={filter}
          onChange={e => setFilter(e.target.value)}
          placeholder="Filter by action, actor, or session ID…"
          className="w-full max-w-md bg-[#0f0f1a] border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-indigo-600"
        />
      </div>

      {/* Error */}
      {error && (
        <div className="mb-6 flex items-center gap-2 px-4 py-3 rounded-lg bg-red-900/20 border border-red-700/40 text-red-400 text-sm">
          <AlertCircle className="w-4 h-4 shrink-0" />
          {error}
        </div>
      )}

      {/* Loading */}
      {loading && (
        <div className="flex items-center gap-2 text-slate-500 text-sm">
          <Loader2 className="w-4 h-4 animate-spin" />
          Loading audit logs…
        </div>
      )}

      {/* Empty */}
      {!loading && filtered.length === 0 && !error && (
        <div className="text-center py-20 text-slate-600">
          <ScrollText className="w-10 h-10 mx-auto mb-3 text-slate-700" />
          <p className="text-sm">{filter ? "No matching events." : "No audit events yet."}</p>
        </div>
      )}

      {/* Table */}
      {!loading && filtered.length > 0 && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500">
                <th className="text-left px-5 py-3 font-medium">Time</th>
                <th className="text-left px-5 py-3 font-medium">Action</th>
                <th className="text-left px-5 py-3 font-medium">Actor</th>
                <th className="text-left px-5 py-3 font-medium">Session</th>
                <th className="px-5 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/60">
              {filtered.map(log => (
                <>
                  <tr
                    key={log.id}
                    className="hover:bg-slate-800/20 transition-colors cursor-pointer"
                    onClick={() => log.metadata && setExpanded(expanded === log.id ? null : log.id)}
                  >
                    <td className="px-5 py-2.5 text-xs text-slate-500 font-mono whitespace-nowrap">
                      {new Date(log.timestamp).toLocaleString()}
                    </td>
                    <td className="px-5 py-2.5">
                      <span className={`px-2 py-0.5 rounded border text-xs font-mono ${actionColor(log.action)}`}>
                        {log.action}
                      </span>
                    </td>
                    <td className="px-5 py-2.5 text-xs text-slate-400">{log.actor}</td>
                    <td className="px-5 py-2.5 text-xs text-slate-600 font-mono">
                      {log.session_id?.slice(0, 12) ?? "—"}
                    </td>
                    <td className="px-5 py-2.5 text-xs text-slate-700">
                      {log.metadata ? (expanded === log.id ? "▲" : "▼") : ""}
                    </td>
                  </tr>
                  {expanded === log.id && log.metadata && (
                    <tr key={`${log.id}-expanded`} className="bg-[#07070d]">
                      <td colSpan={5} className="px-5 py-3">
                        <pre className="text-xs font-mono text-slate-400 whitespace-pre-wrap">
                          {JSON.stringify(log.metadata, null, 2)}
                        </pre>
                      </td>
                    </tr>
                  )}
                </>
              ))}
            </tbody>
          </table>
          <div className="px-5 py-3 border-t border-slate-800 text-xs text-slate-600">
            {filtered.length} event{filtered.length !== 1 ? "s" : ""}
            {filter && logs.length !== filtered.length && ` (filtered from ${logs.length})`}
          </div>
        </div>
      )}
    </div>
  );
}
