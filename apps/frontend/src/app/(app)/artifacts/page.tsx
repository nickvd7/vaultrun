"use client";

import { useEffect, useState } from "react";
import { Package, Download, Trash2, RefreshCw } from "lucide-react";
import { api } from "@/lib/api";
import { formatBytes } from "@/lib/utils";
import { Time } from "@/components/ui/Time";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Notice } from "@/components/ui/Notice";
import { useToast } from "@/components/ui/Toast";
import type { SharedArtifact } from "@/types";

export default function ArtifactsPage() {
  const [artifacts, setArtifacts] = useState<SharedArtifact[]>([]);
  const [loading, setLoading] = useState(true);
  const toast = useToast();
  const [loadError, setLoadError] = useState("");
  const [toDelete, setToDelete] = useState<SharedArtifact | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [filter, setFilter] = useState("");

  const load = () => {
    setLoading(true);
    setLoadError("");
    api.artifacts
      .list()
      .then(({ artifacts }) => setArtifacts(artifacts))
      .catch((e: unknown) => setLoadError(e instanceof Error ? e.message : "Failed to load artifacts"))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const handleDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    try {
      await api.artifacts.delete(toDelete.id);
      setArtifacts((prev) => prev.filter((a) => a.id !== toDelete.id));
      toast.success(`Artifact ${toDelete.name} deleted`);
      setToDelete(null);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to delete artifact");
    } finally {
      setDeleting(false);
    }
  };

  const handleDownload = async (a: SharedArtifact) => {
    try {
      const resp = await api.artifacts.download(a.id);
      if (!resp.ok) throw new Error(`Download failed (HTTP ${resp.status})`);
      const url = URL.createObjectURL(await resp.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = a.name;
      link.click();
      URL.revokeObjectURL(url);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Download failed");
    }
  };

  const filtered = filter
    ? artifacts.filter((a) => a.name.toLowerCase().includes(filter.toLowerCase()) || a.content_type.includes(filter))
    : artifacts;

  const totalSize = artifacts.reduce((sum, a) => sum + a.size_bytes, 0);

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Artifacts</h1>
          <p className="text-slate-300 text-sm mt-1">
            {artifacts.length} artifact{artifacts.length !== 1 ? "s" : ""} · {formatBytes(totalSize)} total
          </p>
        </div>
        <button onClick={load} aria-label="Refresh artifacts" title="Refresh" className="p-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-300 hover:text-slate-100 transition-colors">
          <RefreshCw className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
        </button>
      </div>

      {loadError && <Notice tone="error" title="Could not load artifacts">{loadError}</Notice>}

      {/* Search */}
      {artifacts.length > 0 && (
        <input
          type="search"
          aria-label="Filter artifacts by name or type"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by name or type…"
          className="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-400"
        />
      )}

      {/* Artifacts list */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-300 text-sm">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-10 text-center space-y-2">
            <Package className="w-8 h-8 text-slate-300 mx-auto" />
            <p className="text-slate-300 text-sm">
              {filter ? "No artifacts match your filter." : "No shared artifacts yet."}
            </p>
            <p className="text-slate-300 text-xs">
              Use <code className="font-mono">create_artifact</code> in the MCP server or the API to promote workspace files.
            </p>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-300 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Type</th>
                <th className="px-4 py-3 text-left">Size</th>
                <th className="px-4 py-3 text-left">Created by</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th scope="col" className="px-4 py-3"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {filtered.map((a) => (
                <tr key={a.id} className="hover:bg-slate-800/20 transition-colors">
                  <td className="px-4 py-3 text-slate-200 text-sm font-medium">{a.name}</td>
                  <td className="px-4 py-3 text-slate-300 text-xs font-mono">{a.content_type || "—"}</td>
                  <td className="px-4 py-3 text-slate-300 text-xs font-mono">{formatBytes(a.size_bytes)}</td>
                  <td className="px-4 py-3 text-slate-300 text-xs font-mono">{a.created_by}</td>
                  <td className="px-4 py-3 text-slate-300 text-xs"><Time value={a.created_at} /></td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => handleDownload(a)}
                        className="p-1.5 rounded text-slate-300 hover:text-indigo-300 hover:bg-indigo-900/20 transition-colors"
                        title="Download"
                        aria-label={`Download artifact ${a.name}`}
                      >
                        <Download className="w-3.5 h-3.5" aria-hidden="true" />
                      </button>
                      <button
                        onClick={() => setToDelete(a)}
                        className="p-1.5 rounded text-slate-300 hover:text-red-400 hover:bg-red-900/20 transition-colors"
                        title="Delete"
                        aria-label={`Delete artifact ${a.name}`}
                      >
                        <Trash2 className="w-3.5 h-3.5" aria-hidden="true" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <ConfirmDialog
        open={toDelete !== null}
        title="Delete artifact?"
        message={
          <>
            Artifact <span className="font-mono">{toDelete?.name}</span> will be removed for everyone who can see it.
          </>
        }
        confirmLabel="Delete artifact"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setToDelete(null)}
      />
    </div>
  );
}
