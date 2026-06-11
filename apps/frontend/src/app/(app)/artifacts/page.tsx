"use client";

import { useEffect, useState } from "react";
import { Package, Download, Trash2, RefreshCw } from "lucide-react";
import { api } from "@/lib/api";
import { formatBytes, relativeTime } from "@/lib/utils";
import type { SharedArtifact } from "@/types";

export default function ArtifactsPage() {
  const [artifacts, setArtifacts] = useState<SharedArtifact[]>([]);
  const [loading, setLoading] = useState(true);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  const load = () => {
    setLoading(true);
    api.artifacts.list().then(({ artifacts }) => setArtifacts(artifacts)).catch(() => {}).finally(() => setLoading(false));
  };

  useEffect(load, []);

  const handleDelete = async (id: string) => {
    setDeletingId(id);
    try {
      await api.artifacts.delete(id);
      setArtifacts((prev) => prev.filter((a) => a.id !== id));
    } finally {
      setDeletingId(null);
    }
  };

  const filtered = filter
    ? artifacts.filter((a) => a.name.toLowerCase().includes(filter.toLowerCase()) || a.content_type.includes(filter))
    : artifacts;

  const totalSize = artifacts.reduce((sum, a) => sum + a.size_bytes, 0);

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Artifacts</h1>
          <p className="text-slate-500 text-sm mt-1">
            {artifacts.length} artifact{artifacts.length !== 1 ? "s" : ""} · {formatBytes(totalSize)} total
          </p>
        </div>
        <button onClick={load} className="p-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 hover:text-slate-200 transition-colors">
          <RefreshCw className="w-4 h-4" />
        </button>
      </div>

      {/* Search */}
      {artifacts.length > 0 && (
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter by name or type…"
          className="w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-600"
        />
      )}

      {/* Artifacts list */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-600 text-sm">Loading…</div>
        ) : filtered.length === 0 ? (
          <div className="p-10 text-center space-y-2">
            <Package className="w-8 h-8 text-slate-700 mx-auto" />
            <p className="text-slate-600 text-sm">
              {filter ? "No artifacts match your filter." : "No shared artifacts yet."}
            </p>
            <p className="text-slate-700 text-xs">
              Use <code className="font-mono">create_artifact</code> in the MCP server or the API to promote workspace files.
            </p>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Type</th>
                <th className="px-4 py-3 text-left">Size</th>
                <th className="px-4 py-3 text-left">Created by</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {filtered.map((a) => (
                <tr key={a.id} className="hover:bg-slate-800/20 transition-colors">
                  <td className="px-4 py-3 text-slate-200 text-sm font-medium">{a.name}</td>
                  <td className="px-4 py-3 text-slate-500 text-xs font-mono">{a.content_type || "—"}</td>
                  <td className="px-4 py-3 text-slate-500 text-xs font-mono">{formatBytes(a.size_bytes)}</td>
                  <td className="px-4 py-3 text-slate-500 text-xs font-mono">{a.created_by}</td>
                  <td className="px-4 py-3 text-slate-500 text-xs">{relativeTime(a.created_at)}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <a
                        href={api.artifacts.downloadUrl(a.id)}
                        download={a.name}
                        className="p-1.5 rounded text-slate-600 hover:text-indigo-400 hover:bg-indigo-900/20 transition-colors"
                        title="Download"
                      >
                        <Download className="w-3.5 h-3.5" />
                      </a>
                      <button
                        onClick={() => handleDelete(a.id)}
                        disabled={deletingId === a.id}
                        className="p-1.5 rounded text-slate-600 hover:text-red-400 hover:bg-red-900/20 disabled:opacity-40 transition-colors"
                        title="Delete"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
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
