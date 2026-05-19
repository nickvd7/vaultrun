"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Plus, Trash2, RefreshCw } from "lucide-react";
import { api } from "@/lib/api";
import { StatusBadge } from "@/components/StatusBadge";
import type { Session } from "@/types";

export default function SessionsPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState({
    image: "python:3.12-slim",
    name: "",
    cpu: "1",
    mem: "512",
    network: false,
  });

  const load = () => {
    setLoading(true);
    api.sessions
      .list()
      .then(setSessions)
      .catch(console.error)
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const handleCreate = async () => {
    setCreating(true);
    try {
      await api.sessions.create({
        name: form.name || undefined,
        image: form.image,
        cpu_limit: parseFloat(form.cpu),
        memory_limit_mb: parseInt(form.mem),
        network_enabled: form.network,
      });
      setShowForm(false);
      load();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Create failed");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this session?")) return;
    await api.sessions.delete(id).catch(console.error);
    load();
  };

  return (
    <div className="max-w-5xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Sessions</h1>
          <p className="text-slate-500 text-sm mt-1">
            Isolated Docker sandbox sessions
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={load}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-400 border border-slate-700 rounded-md hover:bg-slate-800"
          >
            <RefreshCw className="w-3.5 h-3.5" /> Refresh
          </button>
          <button
            onClick={() => setShowForm(!showForm)}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-white bg-indigo-600 rounded-md hover:bg-indigo-500"
          >
            <Plus className="w-3.5 h-3.5" /> New Session
          </button>
        </div>
      </div>

      {/* Create form */}
      {showForm && (
        <div className="bg-[#0f0f1a] border border-slate-700 rounded-lg p-5 space-y-4">
          <h2 className="text-sm font-medium text-slate-300">
            Create New Session
          </h2>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs text-slate-500 mb-1">Name (optional)</label>
              <input
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="my-agent-session"
              />
            </div>
            <div>
              <label className="block text-xs text-slate-500 mb-1">Image</label>
              <input
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 font-mono"
                value={form.image}
                onChange={(e) => setForm({ ...form, image: e.target.value })}
              />
            </div>
            <div>
              <label className="block text-xs text-slate-500 mb-1">
                CPU Limit (cores)
              </label>
              <input
                type="number"
                min="0.1"
                max="8"
                step="0.1"
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
                value={form.cpu}
                onChange={(e) => setForm({ ...form, cpu: e.target.value })}
              />
            </div>
            <div>
              <label className="block text-xs text-slate-500 mb-1">
                Memory (MB)
              </label>
              <input
                type="number"
                min="64"
                max="32768"
                step="64"
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
                value={form.mem}
                onChange={(e) => setForm({ ...form, mem: e.target.value })}
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="network"
              checked={form.network}
              onChange={(e) => setForm({ ...form, network: e.target.checked })}
              className="rounded"
            />
            <label htmlFor="network" className="text-sm text-slate-400">
              Enable network access
            </label>
          </div>
          <div className="flex gap-2 pt-2">
            <button
              onClick={handleCreate}
              disabled={creating}
              className="px-4 py-2 text-sm text-white bg-indigo-600 rounded hover:bg-indigo-500 disabled:opacity-50"
            >
              {creating ? "Creating…" : "Create Session"}
            </button>
            <button
              onClick={() => setShowForm(false)}
              className="px-4 py-2 text-sm text-slate-400 border border-slate-700 rounded hover:bg-slate-800"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Sessions table */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-600 text-sm">Loading…</div>
        ) : sessions.length === 0 ? (
          <div className="p-8 text-center text-slate-600 text-sm">
            No sessions. Click &quot;New Session&quot; to create one.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">ID</th>
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Image</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Resources</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {sessions.map((s) => (
                <tr
                  key={s.id}
                  className="hover:bg-slate-800/20 transition-colors"
                >
                  <td className="px-4 py-3">
                    <Link
                      href={`/sessions/${s.id}`}
                      className="font-mono text-indigo-400 hover:text-indigo-300 text-xs"
                    >
                      {s.id.slice(0, 8)}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-slate-300 text-xs">
                    {s.name || <span className="text-slate-600">—</span>}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-slate-400">
                    {s.image}
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge status={s.status} />
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-500 font-mono">
                    {s.cpu_limit}CPU / {s.memory_limit_mb}MB
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-500">
                    {new Date(s.created_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => handleDelete(s.id)}
                      className="text-slate-600 hover:text-red-400 transition-colors"
                    >
                      <Trash2 className="w-4 h-4" />
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
