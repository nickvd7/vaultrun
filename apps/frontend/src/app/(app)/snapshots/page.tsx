"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Archive, Plus, Trash2, RefreshCw, Download } from "lucide-react";
import { api } from "@/lib/api";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Notice } from "@/components/ui/Notice";
import { useToast } from "@/components/ui/Toast";
import { formatBytes } from "@/lib/utils";
import { Time } from "@/components/ui/Time";
import type { Session, Snapshot } from "@/types";

const MAX_SESSIONS = 100;

export default function SnapshotsPage() {
  const toast = useToast();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [toDelete, setToDelete] = useState<Snapshot | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(() => {
    setLoading(true);
    setLoadError("");
    api.sessions
      .list(1, MAX_SESSIONS)
      .then(async ({ sessions: list }) => {
        setSessions(list);
        const results = await Promise.allSettled(list.map((s) => api.snapshots.list(s.id)));
        setSnapshots(
          results
            .flatMap((r) => (r.status === "fulfilled" ? r.value : []))
            .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
        );
      })
      .catch((e: unknown) => setLoadError(e instanceof Error ? e.message : "Failed to load snapshots"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(load, [load]);

  const sessionLabel = (id: string) => {
    const s = sessions.find((x) => x.id === id);
    return s?.name || `${id.slice(0, 8)}…`;
  };

  const handleCreate = async () => {
    if (!selectedId || !newName.trim()) return;
    setCreating(true);
    try {
      const snap = await api.snapshots.create(selectedId, newName.trim());
      setSnapshots((prev) => [snap, ...prev]);
      setNewName("");
      toast.success(`Snapshot ${snap.name} saved`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to create snapshot");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    try {
      await api.snapshots.delete(toDelete.id);
      setSnapshots((prev) => prev.filter((s) => s.id !== toDelete.id));
      toast.success(`Snapshot ${toDelete.name} deleted`);
      setToDelete(null);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to delete snapshot");
    } finally {
      setDeleting(false);
    }
  };

  const handleDownload = async (snap: Snapshot) => {
    try {
      const resp = await api.snapshots.download(snap.id);
      if (!resp.ok) throw new Error(`Download failed (HTTP ${resp.status})`);
      const url = URL.createObjectURL(await resp.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `${snap.name}.tar.gz`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Download failed");
    }
  };

  const running = sessions.filter((s) => s.status === "running");

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Snapshots</h1>
          <p className="text-slate-300 text-sm mt-1">Archives of a session&apos;s workspace files</p>
        </div>
        <button
          onClick={load}
          disabled={loading}
          aria-label="Refresh snapshots"
          title="Refresh"
          className="p-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-300 hover:text-slate-100 transition-colors disabled:opacity-60"
        >
          <RefreshCw className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
        </button>
      </div>

      <Notice title="What is a snapshot?">
        A snapshot is a compressed copy (<span className="font-mono">.tar.gz</span>) of everything in a session&apos;s{" "}
        <span className="font-mono">/workspace</span> at one moment. Use it to keep a backup or to download the results of a run before deleting the session.
        To roll a session back to an earlier state, use{" "}
        <Link href="/sessions" className="text-indigo-300 underline hover:no-underline">checkpoints</Link> on the session page.
      </Notice>

      {loadError && <Notice tone="error" title="Could not load snapshots">{loadError}</Notice>}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          handleCreate();
        }}
        className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-4 space-y-3"
      >
        <h2 className="text-xs font-medium text-slate-200 uppercase tracking-wide">New snapshot</h2>
        <div className="flex flex-col sm:flex-row gap-3">
          <div className="flex-1">
            <label htmlFor="snap-session" className="block text-xs text-slate-200 mb-1">Session</label>
            <select
              id="snap-session"
              value={selectedId}
              onChange={(e) => setSelectedId(e.target.value)}
              disabled={loading}
              className="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 disabled:opacity-60"
            >
              <option value="">{running.length === 0 ? "No running sessions" : "Choose a running session…"}</option>
              {running.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name || s.id.slice(0, 12)} · {s.image}
                </option>
              ))}
            </select>
          </div>
          <div className="flex-1">
            <label htmlFor="snap-name" className="block text-xs text-slate-200 mb-1">Snapshot name</label>
            <input
              id="snap-name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              placeholder="before-refactor"
              className="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-400"
            />
          </div>
          <div className="flex items-end">
            <button
              type="submit"
              disabled={creating || !selectedId || !newName.trim()}
              className="flex items-center gap-2 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:opacity-60 text-white text-sm font-medium transition-colors whitespace-nowrap"
            >
              <Plus className="w-4 h-4" aria-hidden="true" /> {creating ? "Saving…" : "Save Snapshot"}
            </button>
          </div>
        </div>
      </form>

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-300 text-sm" role="status">Loading snapshots…</div>
        ) : snapshots.length === 0 ? (
          <div className="p-10 text-center space-y-2">
            <Archive className="w-8 h-8 text-slate-300 mx-auto" aria-hidden="true" />
            <p className="text-slate-300 text-sm">No snapshots yet.</p>
            <p className="text-slate-200 text-xs">Pick a running session above and give the snapshot a name.</p>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
                <th scope="col" className="px-4 py-3 text-left font-medium">Name</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Session</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Size</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Created by</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Created</th>
                <th scope="col" className="px-4 py-3">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {snapshots.map((snap) => (
                <tr key={snap.id} className="hover:bg-slate-800/20 transition-colors">
                  <td className="px-4 py-3 text-slate-200 text-sm">{snap.name}</td>
                  <td className="px-4 py-3 font-mono text-xs">
                    <Link href={`/sessions/${snap.session_id}`} className="text-indigo-300 hover:underline">
                      {sessionLabel(snap.session_id)}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-slate-200 text-xs font-mono">{formatBytes(snap.size_bytes)}</td>
                  <td className="px-4 py-3 text-slate-200 text-xs font-mono" title={snap.created_by_name ? `${snap.created_by_name} (${snap.created_by})` : snap.created_by}>
                    {(() => {
                      const who = snap.created_by_name || snap.created_by;
                      return who.length > 20 ? who.slice(0, 20) + "…" : who;
                    })()}
                  </td>
                  <td className="px-4 py-3 text-slate-200 text-xs"><Time value={snap.created_at} /></td>
                  <td className="px-4 py-3 text-right whitespace-nowrap">
                    <button
                      onClick={() => handleDownload(snap)}
                      aria-label={`Download snapshot ${snap.name}`}
                      title="Download"
                      className="p-1.5 rounded text-slate-300 hover:text-slate-100 hover:bg-slate-800 transition-colors"
                    >
                      <Download className="w-3.5 h-3.5" aria-hidden="true" />
                    </button>
                    <button
                      onClick={() => setToDelete(snap)}
                      aria-label={`Delete snapshot ${snap.name}`}
                      title="Delete"
                      className="p-1.5 rounded text-slate-300 hover:text-red-400 hover:bg-red-900/20 transition-colors"
                    >
                      <Trash2 className="w-3.5 h-3.5" aria-hidden="true" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <ConfirmDialog
        open={toDelete !== null}
        title="Delete snapshot?"
        message={
          <>
            Snapshot <span className="font-mono">{toDelete?.name}</span> will be removed permanently.
          </>
        }
        confirmLabel="Delete snapshot"
        busyLabel="Deleting…"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setToDelete(null)}
      />
    </div>
  );
}
