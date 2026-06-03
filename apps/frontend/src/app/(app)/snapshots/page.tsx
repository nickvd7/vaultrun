"use client";

import { useEffect, useState } from "react";
import { Archive, Plus, Trash2, RefreshCw, ChevronDown } from "lucide-react";
import { api } from "@/lib/api";
import { formatBytes, relativeTime } from "@/lib/utils";
import type { Session, Snapshot } from "@/types";

export default function SnapshotsPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [loadingSessions, setLoadingSessions] = useState(true);
  const [loadingSnaps, setLoadingSnaps] = useState(false);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.sessions.list(1, 100).then(({ sessions }) => setSessions(sessions)).catch(() => {}).finally(() => setLoadingSessions(false));
  }, []);

  const loadSnapshots = (sessionId: string) => {
    if (!sessionId) return;
    setLoadingSnaps(true);
    setSnapshots([]);
    api.snapshots.list(sessionId).then(setSnapshots).catch(() => {}).finally(() => setLoadingSnaps(false));
  };

  const handleSelectSession = (id: string) => {
    setSelectedId(id);
    loadSnapshots(id);
  };

  const handleCreate = async () => {
    if (!selectedId || !newName.trim()) return;
    setCreating(true);
    setError("");
    try {
      const snap = await api.snapshots.create(selectedId, newName.trim());
      setSnapshots((prev) => [snap, ...prev]);
      setNewName("");
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create snapshot");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    setDeletingId(id);
    try {
      await api.snapshots.delete(id);
      setSnapshots((prev) => prev.filter((s) => s.id !== id));
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-slate-100">Snapshots</h1>
        <p className="text-slate-500 text-sm mt-1">Workspace checkpoints — save and restore session state.</p>
      </div>

      {/* Session selector */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-4">
        <label className="block text-xs text-slate-500 mb-2 uppercase tracking-wide">Select a session</label>
        <div className="relative">
          <select
            value={selectedId}
            onChange={(e) => handleSelectSession(e.target.value)}
            disabled={loadingSessions}
            className="w-full appearance-none bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 pr-9 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 disabled:opacity-50"
          >
            <option value="">— choose a session —</option>
            {sessions.map((s) => (
              <option key={s.id} value={s.id}>
                {s.id.slice(0, 12)}… {s.name ? `(${s.name})` : ""} · {s.image} · {s.status}
              </option>
            ))}
          </select>
          <ChevronDown className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
        </div>
      </div>

      {/* Create snapshot */}
      {selectedId && (
        <div className="flex items-center gap-3">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleCreate()}
            placeholder="Snapshot name…"
            className="flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-600"
          />
          <button
            onClick={handleCreate}
            disabled={creating || !newName.trim()}
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white text-sm font-medium transition-colors"
          >
            <Plus className="w-4 h-4" /> {creating ? "Saving…" : "Save Snapshot"}
          </button>
          <button onClick={() => loadSnapshots(selectedId)} className="p-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 hover:text-slate-200 transition-colors">
            <RefreshCw className="w-4 h-4" />
          </button>
        </div>
      )}
      {error && <p className="text-red-400 text-xs">{error}</p>}

      {/* Snapshots list */}
      {selectedId && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
          {loadingSnaps ? (
            <div className="p-8 text-center text-slate-600 text-sm">Loading snapshots…</div>
          ) : snapshots.length === 0 ? (
            <div className="p-10 text-center space-y-2">
              <Archive className="w-8 h-8 text-slate-700 mx-auto" />
              <p className="text-slate-600 text-sm">No snapshots for this session yet.</p>
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                  <th className="px-4 py-3 text-left">Name</th>
                  <th className="px-4 py-3 text-left">Size</th>
                  <th className="px-4 py-3 text-left">Created by</th>
                  <th className="px-4 py-3 text-left">Created</th>
                  <th className="px-4 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800/50">
                {snapshots.map((snap) => (
                  <tr key={snap.id} className="hover:bg-slate-800/20 transition-colors">
                    <td className="px-4 py-3 text-slate-200 text-sm">{snap.name}</td>
                    <td className="px-4 py-3 text-slate-500 text-xs font-mono">{formatBytes(snap.size_bytes)}</td>
                    <td className="px-4 py-3 text-slate-500 text-xs font-mono">{snap.created_by}</td>
                    <td className="px-4 py-3 text-slate-500 text-xs">{relativeTime(snap.created_at)}</td>
                    <td className="px-4 py-3 text-right">
                      <button
                        onClick={() => handleDelete(snap.id)}
                        disabled={deletingId === snap.id}
                        className="p-1.5 rounded text-slate-600 hover:text-red-400 hover:bg-red-900/20 disabled:opacity-40 transition-colors"
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
      )}
    </div>
  );
}
