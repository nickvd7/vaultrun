"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Plus, Trash2, RefreshCw, Terminal } from "lucide-react";
import { api } from "@/lib/api";
import { StatusBadge } from "@/components/StatusBadge";
import { relativeTime } from "@/lib/utils";
import { useOrg } from "@/components/OrgContext";
import type { Session } from "@/types";

const DEFAULT_IMAGE = "python:3.12-slim";

export default function SessionsPage() {
  const { orgs, orgId } = useOrg();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({
    name: "",
    image: DEFAULT_IMAGE,
    network_enabled: false,
    cpu_limit: "1",
    memory_limit_mb: "512",
    timeout_seconds: "300",
    org_id: "",
    replay_enabled: false,
  });
  const [error, setError] = useState("");
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.sessions
      .list(1, 50)
      .then(({ sessions: list }) => {
        if (orgId) setSessions(list.filter((s) => s.org_id === orgId));
        else setSessions(list);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(load, [orgId]);

  useEffect(() => {
    if (orgId) setForm((f) => ({ ...f, org_id: orgId }));
  }, [orgId]);

  const handleCreate = async () => {
    setCreating(true);
    setError("");
    try {
      await api.sessions.create({
        name: form.name || undefined,
        image: form.image || DEFAULT_IMAGE,
        network_enabled: form.network_enabled,
        cpu_limit: parseFloat(form.cpu_limit) || 1,
        memory_limit_mb: parseInt(form.memory_limit_mb) || 512,
        timeout_seconds: parseInt(form.timeout_seconds) || 300,
        org_id: form.org_id || undefined,
        replay_enabled: form.replay_enabled,
      });
      setShowForm(false);
      setForm({
        name: "",
        image: DEFAULT_IMAGE,
        network_enabled: false,
        cpu_limit: "1",
        memory_limit_mb: "512",
        timeout_seconds: "300",
        org_id: orgId ?? "",
        replay_enabled: false,
      });
      load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create session");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    setDeletingId(id);
    try {
      await api.sessions.delete(id);
      setSessions((prev) => prev.filter((s) => s.id !== id));
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <div className="max-w-5xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Sessions</h1>
          <p className="text-slate-500 text-sm mt-1">
            {sessions.length} session{sessions.length !== 1 ? "s" : ""}
            {orgId ? " in selected org" : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={load} className="p-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 hover:text-slate-200 transition-colors" title="Refresh">
            <RefreshCw className="w-4 h-4" />
          </button>
          <button
            onClick={() => setShowForm(true)}
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
          >
            <Plus className="w-4 h-4" /> New Session
          </button>
        </div>
      </div>

      {showForm && (
        <div className="bg-[#0f0f1a] border border-slate-700 rounded-xl p-6 space-y-4">
          <h2 className="text-sm font-medium text-slate-200">New Session</h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <Field label="Name (optional)">
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="my-sandbox" className={input} />
            </Field>
            <Field label="Docker Image">
              <input value={form.image} onChange={(e) => setForm({ ...form, image: e.target.value })} placeholder={DEFAULT_IMAGE} className={input} />
            </Field>
            <Field label="CPU Limit">
              <input value={form.cpu_limit} onChange={(e) => setForm({ ...form, cpu_limit: e.target.value })} placeholder="1.0" className={input} />
            </Field>
            <Field label="Memory (MB)">
              <input value={form.memory_limit_mb} onChange={(e) => setForm({ ...form, memory_limit_mb: e.target.value })} placeholder="512" className={input} />
            </Field>
            <Field label="Timeout (seconds)">
              <input value={form.timeout_seconds} onChange={(e) => setForm({ ...form, timeout_seconds: e.target.value })} placeholder="300" className={input} />
            </Field>
            {orgs.length > 0 && (
              <Field label="Organization">
                <select
                  value={form.org_id}
                  onChange={(e) => setForm({ ...form, org_id: e.target.value })}
                  className={input}
                >
                  <option value="">Personal (no org)</option>
                  {orgs.map((o) => (
                    <option key={o.id} value={o.id}>
                      {o.name}
                    </option>
                  ))}
                </select>
              </Field>
            )}
            <Field label="Network">
              <label className="flex items-center gap-2 mt-2 cursor-pointer">
                <input type="checkbox" checked={form.network_enabled} onChange={(e) => setForm({ ...form, network_enabled: e.target.checked })} className="accent-indigo-500" />
                <span className="text-slate-300 text-sm">Enable network access</span>
              </label>
            </Field>
            <Field label="Replay">
              <label className="flex items-center gap-2 mt-2 cursor-pointer">
                <input type="checkbox" checked={form.replay_enabled} onChange={(e) => setForm({ ...form, replay_enabled: e.target.checked })} className="accent-indigo-500" />
                <span className="text-slate-300 text-sm">Enable session replay checkpoints</span>
              </label>
            </Field>
          </div>
          {error && <p className="text-red-400 text-xs">{error}</p>}
          <div className="flex items-center gap-3 pt-1">
            <button onClick={handleCreate} disabled={creating} className="px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white text-sm font-medium transition-colors">
              {creating ? "Creating…" : "Create"}
            </button>
            <button onClick={() => { setShowForm(false); setError(""); }} className="px-4 py-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 text-sm transition-colors">
              Cancel
            </button>
          </div>
        </div>
      )}

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-600 text-sm">Loading…</div>
        ) : sessions.length === 0 ? (
          <div className="p-10 text-center space-y-3">
            <Terminal className="w-8 h-8 text-slate-700 mx-auto" />
            <p className="text-slate-600 text-sm">No sessions yet. Create one to get started.</p>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Session</th>
                <th className="px-4 py-3 text-left">Image</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Resources</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {sessions.map((s) => (
                <tr key={s.id} className="hover:bg-slate-800/20 transition-colors">
                  <td className="px-4 py-3">
                    <Link href={`/sessions/${s.id}`} className="font-mono text-indigo-400 text-xs hover:underline">
                      {s.name || `${s.id.slice(0, 12)}…`}
                    </Link>
                    {s.replay_enabled && (
                      <span className="ml-2 text-[10px] uppercase tracking-wide text-slate-600">replay</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-slate-300 font-mono text-xs">{s.image}</td>
                  <td className="px-4 py-3"><StatusBadge status={s.status} /></td>
                  <td className="px-4 py-3 text-xs text-slate-500">{s.cpu_limit}CPU · {s.memory_limit_mb}MB</td>
                  <td className="px-4 py-3 text-xs text-slate-500">{relativeTime(s.created_at)}</td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => handleDelete(s.id)}
                      disabled={deletingId === s.id}
                      className="p-1.5 rounded text-slate-600 hover:text-red-400 hover:bg-red-900/20 disabled:opacity-40 transition-colors"
                      title="Delete session"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
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

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-xs text-slate-500 mb-1.5">{label}</label>
      {children}
    </div>
  );
}

const input = "w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-600";
