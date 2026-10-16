"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Terminal, Activity, Shield, HardDrive, Archive, Package } from "lucide-react";
import { api } from "@/lib/api";
import { StatusBadge } from "@/components/StatusBadge";
import { Notice } from "@/components/ui/Notice";
import { Time } from "@/components/ui/Time";
import type { Session } from "@/types";

export default function DashboardPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const router = useRouter();

  useEffect(() => {
    api.sessions
      .list(1, 200)
      .then(({ sessions, pagination }) => {
        setSessions(sessions);
        setTotal(pagination?.total ?? sessions.length);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "Could not load sessions"))
      .finally(() => setLoading(false));
  }, []);

  const running = sessions.filter((s) => s.status === "running").length;
  const stopped = sessions.filter((s) => s.status === "stopped").length;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Dashboard</h1>
        <p className="text-slate-300 text-sm mt-1">
          VaultRun — self-hosted secure AI agent runtime
        </p>
      </div>

      {error && (
        <Notice tone="error" title="Could not load sessions">
          {error}
        </Notice>
      )}

      {/* Stats */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {[
          { label: "Active Sessions", value: running, icon: Terminal, color: "text-green-400" },
          { label: "Total Sessions", value: total, icon: Activity, color: "text-indigo-400" },
          { label: "Stopped", value: stopped, icon: HardDrive, color: "text-slate-300" },
          { label: "Security", value: "Isolated", icon: Shield, color: "text-emerald-400" },
        ].map(({ label, value, icon: Icon, color }) => (
          <div key={label} className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-4">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs text-slate-300 uppercase tracking-wide">{label}</span>
              <Icon className={`w-4 h-4 ${color}`} />
            </div>
            <div className={`text-2xl font-semibold ${color}`}>{value}</div>
          </div>
        ))}
      </div>

      {/* Quick links */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        {[
          { href: "/sessions", icon: Terminal, label: "Sessions", desc: "Manage sandbox containers" },
          { href: "/snapshots", icon: Archive, label: "Snapshots", desc: "Workspace archives" },
          { href: "/artifacts", icon: Package, label: "Artifacts", desc: "Shared output files" },
        ].map(({ href, icon: Icon, label, desc }) => (
          <Link
            key={href}
            href={href}
            className="bg-[#0f0f1a] border border-slate-800 hover:border-slate-700 rounded-lg p-4 flex items-center gap-4 transition-colors group"
          >
            <div className="flex items-center justify-center w-9 h-9 rounded-lg bg-indigo-900/30 border border-indigo-800/40 group-hover:bg-indigo-900/50 transition-colors">
              <Icon className="w-4 h-4 text-indigo-400" />
            </div>
            <div>
              <div className="text-sm font-medium text-slate-200">{label}</div>
              <div className="text-xs text-slate-300">{desc}</div>
            </div>
          </Link>
        ))}
      </div>

      {/* Recent sessions */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-medium text-slate-300 uppercase tracking-wide">
            Recent Sessions
          </h2>
          <Link href="/sessions" className="text-xs text-indigo-400 hover:text-indigo-300">
            View all →
          </Link>
        </div>

        <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
          {loading ? (
            <div className="p-6 text-center text-slate-300 text-sm" role="status">Loading…</div>
          ) : sessions.length === 0 ? (
            <div className="p-6 text-center text-slate-300 text-sm">
              No sessions yet.{" "}
              <Link href="/sessions" className="text-indigo-400 hover:underline">
                Create one
              </Link>
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-800 text-xs text-slate-300 uppercase tracking-wide">
                  <th scope="col" className="px-4 py-3 text-left">Session</th>
                  <th className="px-4 py-3 text-left">Image</th>
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Created</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/50">
                {sessions.slice(0, 10).map((s) => (
                  <tr
                    key={s.id}
                    onClick={() => router.push(`/sessions/${s.id}`)}
                    className="hover:bg-slate-800/20 transition-colors cursor-pointer"
                  >
                    <td className="px-4 py-3">
                      <Link
                        href={`/sessions/${s.id}`}
                        onClick={(e) => e.stopPropagation()}
                        className="font-mono text-indigo-300 text-xs hover:underline"
                      >
                        {s.name || `${s.id.slice(0, 8)}…`}
                      </Link>
                    </td>
                    <td className="px-4 py-3 text-slate-300 font-mono text-xs">{s.image}</td>
                    <td className="px-4 py-3">
                      <StatusBadge status={s.status} />
                    </td>
                    <td className="px-4 py-3 text-slate-300 text-xs">
                      <Time value={s.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
