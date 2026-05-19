"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { formatDate } from "@/lib/utils";
import type { AuditLog } from "@/types";

export default function AuditPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    api.audit
      .list()
      .then(setLogs)
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  const filtered = filter
    ? logs.filter(
        (l) =>
          l.action.includes(filter) ||
          l.actor.includes(filter) ||
          l.session_id?.includes(filter)
      )
    : logs;

  return (
    <div className="max-w-5xl mx-auto space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Audit Logs</h1>
        <p className="text-slate-500 text-sm mt-1">
          Immutable record of all security-relevant events
        </p>
      </div>

      <input
        className="w-full bg-[#0f0f1a] border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
        placeholder="Filter by action, actor, or session ID…"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-600 text-sm">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-8 text-center text-slate-600 text-sm">
            No audit logs found.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Timestamp</th>
                <th className="px-4 py-3 text-left">Actor</th>
                <th className="px-4 py-3 text-left">Action</th>
                <th className="px-4 py-3 text-left">Session</th>
                <th className="px-4 py-3 text-left">Metadata</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {filtered.map((l) => (
                <tr key={l.id} className="hover:bg-slate-800/20">
                  <td className="px-4 py-2.5 text-xs text-slate-500 font-mono whitespace-nowrap">
                    {formatDate(l.timestamp)}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-400">
                    {l.actor}
                  </td>
                  <td className="px-4 py-2.5">
                    <span className="font-mono text-xs text-indigo-300 bg-indigo-900/20 px-2 py-0.5 rounded">
                      {l.action}
                    </span>
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-500">
                    {l.session_id ? l.session_id.slice(0, 8) + "…" : "—"}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-600 truncate max-w-xs">
                    {Object.keys(l.metadata).length > 0
                      ? JSON.stringify(l.metadata)
                      : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
