"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import {
  Plus,
  RefreshCw,
  Box,
  ChevronRight,
  Trash2,
  AlertCircle,
  Loader2,
} from "lucide-react";

const API = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const API_KEY = process.env.NEXT_PUBLIC_API_KEY ?? "";

type Session = {
  id: string;
  name?: string;
  image: string;
  status: string;
  cpu_limit: number;
  memory_limit_mb: number;
  created_at: string;
  network_enabled: boolean;
};

function statusColor(status: string) {
  switch (status) {
    case "running": return "bg-green-500/20 text-green-400 border-green-500/30";
    case "stopped": return "bg-slate-700/40 text-slate-400 border-slate-600/30";
    case "error": return "bg-red-500/20 text-red-400 border-red-500/30";
    default: return "bg-yellow-500/20 text-yellow-400 border-yellow-500/30";
  }
}

async function apiFetch(path: string, opts?: RequestInit) {
  const res = await fetch(`${API}${path}`, {
    ...opts,
    headers: {
      "X-API-Key": API_KEY,
      "Content-Type": "application/json",
      ...(opts?.headers ?? {}),
    },
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error ?? res.statusText);
  }
  return res.status === 204 ? null : res.json();
}

export default function SessionsPage() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [newImage, setNewImage] = useState("python:3.12-slim");
  const [newName, setNewName] = useState("");

  async function load() {
    setLoading(true);
    setError("");
    try {
      const data = await apiFetch("/api/v1/sessions?limit=50");
      setSessions(data.sessions ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  async function createSession() {
    setCreating(true);
    try {
      await apiFetch("/api/v1/sessions", {
        method: "POST",
        body: JSON.stringify({
          image: newImage || "python:3.12-slim",
          name: newName || undefined,
          network_enabled: false,
          cpu_limit: 1.0,
          memory_limit_mb: 512,
          timeout_seconds: 300,
        }),
      });
      setShowCreate(false);
      setNewName("");
      setNewImage("python:3.12-slim");
      await load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  }

  async function deleteSession(id: string) {
    if (!confirm("Delete this session and its workspace?")) return;
    try {
      await apiFetch(`/api/v1/sessions/${id}`, { method: "DELETE" });
      await load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="p-8">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-xl font-semibold text-slate-100">Sessions</h1>
          <p className="text-slate-500 text-sm mt-0.5">Active sandbox containers</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={load}
            className="flex items-center gap-1.5 px-3 py-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 hover:text-slate-200 text-sm transition-colors"
          >
            <RefreshCw className="w-3.5 h-3.5" />
            Refresh
          </button>
          <button
            onClick={() => setShowCreate(true)}
            className="flex items-center gap-1.5 px-3 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
          >
            <Plus className="w-3.5 h-3.5" />
            New Session
          </button>
        </div>
      </div>

      {/* Create dialog */}
      {showCreate && (
        <div className="mb-6 bg-[#0f0f1a] border border-indigo-700/40 rounded-xl p-6">
          <h2 className="text-sm font-medium text-slate-200 mb-4">Create session</h2>
          <div className="grid sm:grid-cols-2 gap-4 mb-4">
            <div>
              <label className="block text-xs text-slate-500 mb-1.5">Name (optional)</label>
              <input
                value={newName}
                onChange={e => setNewName(e.target.value)}
                placeholder="my-session"
                className="w-full bg-[#07070d] border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-indigo-600"
              />
            </div>
            <div>
              <label className="block text-xs text-slate-500 mb-1.5">Docker image</label>
              <input
                value={newImage}
                onChange={e => setNewImage(e.target.value)}
                placeholder="python:3.12-slim"
                className="w-full bg-[#07070d] border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-indigo-600"
              />
            </div>
          </div>
          <div className="flex gap-2">
            <button
              onClick={createSession}
              disabled={creating}
              className="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white text-sm font-medium transition-colors"
            >
              {creating && <Loader2 className="w-3.5 h-3.5 animate-spin" />}
              Create
            </button>
            <button
              onClick={() => setShowCreate(false)}
              className="px-4 py-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 text-sm transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

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
          Loading sessions…
        </div>
      )}

      {/* Empty */}
      {!loading && sessions.length === 0 && !error && (
        <div className="text-center py-20 text-slate-600">
          <Box className="w-10 h-10 mx-auto mb-3 text-slate-700" />
          <p className="text-sm">No sessions yet.</p>
          <p className="text-xs mt-1">Click &quot;New Session&quot; to create your first sandbox.</p>
        </div>
      )}

      {/* Table */}
      {!loading && sessions.length > 0 && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500">
                <th className="text-left px-5 py-3 font-medium">Session</th>
                <th className="text-left px-5 py-3 font-medium">Image</th>
                <th className="text-left px-5 py-3 font-medium">Status</th>
                <th className="text-left px-5 py-3 font-medium">Resources</th>
                <th className="text-left px-5 py-3 font-medium">Created</th>
                <th className="px-5 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/60">
              {sessions.map(s => (
                <tr key={s.id} className="hover:bg-slate-800/20 transition-colors">
                  <td className="px-5 py-3">
                    <Link
                      href={`/dashboard/sessions/${s.id}`}
                      className="flex items-center gap-2 group"
                    >
                      <span className="text-slate-200 font-mono text-xs">
                        {s.name ?? s.id.slice(0, 8)}
                      </span>
                      {s.name && (
                        <span className="text-slate-600 font-mono text-xs">
                          {s.id.slice(0, 8)}
                        </span>
                      )}
                      <ChevronRight className="w-3.5 h-3.5 text-slate-600 group-hover:text-indigo-400 transition-colors" />
                    </Link>
                  </td>
                  <td className="px-5 py-3 font-mono text-xs text-slate-400">{s.image}</td>
                  <td className="px-5 py-3">
                    <span className={`px-2 py-0.5 rounded-full border text-xs ${statusColor(s.status)}`}>
                      {s.status}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-xs text-slate-500">
                    {s.cpu_limit} CPU · {s.memory_limit_mb} MB
                  </td>
                  <td className="px-5 py-3 text-xs text-slate-600">
                    {new Date(s.created_at).toLocaleString()}
                  </td>
                  <td className="px-5 py-3">
                    <button
                      onClick={() => deleteSession(s.id)}
                      className="p-1.5 rounded text-slate-600 hover:text-red-400 hover:bg-red-900/20 transition-colors"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
