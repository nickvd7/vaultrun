"use client";

import { useEffect, useState, useRef } from "react";
import { useParams, useRouter } from "next/navigation";
import {
  ArrowLeft,
  Terminal,
  FolderOpen,
  ScrollText,
  Play,
  Upload,
  Trash2,
  RefreshCw,
  AlertCircle,
  Loader2,
  Download,
  ChevronRight,
} from "lucide-react";

async function apiFetch(path: string, opts?: RequestInit) {
  const res = await fetch(`/api/proxy${path}`, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(opts?.headers ?? {}) },
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error ?? res.statusText);
  }
  return res.status === 204 ? null : res.json();
}

type Session = {
  id: string; name?: string; image: string; status: string;
  cpu_limit: number; memory_limit_mb: number; timeout_seconds: number;
  network_enabled: boolean; created_at: string;
};
type Run = {
  id: string; command: string; args: string[]; status: string;
  exit_code?: number; stdout?: string; stderr?: string;
  duration_ms?: number; created_at: string;
};
type File = { id: string; path: string; size_bytes: number; content_type: string; created_at: string };
type AuditLog = { id: string; actor: string; action: string; timestamp: string; metadata?: Record<string, unknown> };

const TABS = ["Run", "Files", "Runs", "Audit"] as const;
type Tab = typeof TABS[number];

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();

  const [session, setSession] = useState<Session | null>(null);
  const [tab, setTab] = useState<Tab>("Run");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  // Run tab
  const [command, setCommand] = useState("python");
  const [args, setArgs] = useState("");
  const [runResult, setRunResult] = useState<Run | null>(null);
  const [running, setRunning] = useState(false);

  // Files tab
  const [files, setFiles] = useState<File[]>([]);
  const [filesLoading, setFilesLoading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);

  // Runs tab
  const [runs, setRuns] = useState<Run[]>([]);
  const [runsLoading, setRunsLoading] = useState(false);
  const [expandedRun, setExpandedRun] = useState<string | null>(null);

  // Audit tab
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [auditLoading, setAuditLoading] = useState(false);

  useEffect(() => {
    apiFetch(`/api/v1/sessions/${id}`)
      .then(data => setSession(data))
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  }, [id]);

  async function loadFiles() {
    setFilesLoading(true);
    try {
      const data = await apiFetch(`/api/v1/sessions/${id}/files`);
      setFiles(data.files ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setFilesLoading(false);
    }
  }

  async function loadRuns() {
    setRunsLoading(true);
    try {
      const data = await apiFetch(`/api/v1/sessions/${id}/runs`);
      setRuns(data.runs ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setRunsLoading(false);
    }
  }

  async function loadAudit() {
    setAuditLoading(true);
    try {
      const data = await apiFetch(`/api/v1/audit?session_id=${id}&limit=50`);
      setAuditLogs(data.audit_logs ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setAuditLoading(false);
    }
  }

  function handleTabChange(t: Tab) {
    setTab(t);
    if (t === "Files") loadFiles();
    if (t === "Runs") loadRuns();
    if (t === "Audit") loadAudit();
  }

  async function runCommand() {
    setRunning(true);
    setRunResult(null);
    setError("");
    try {
      const data = await apiFetch(`/api/v1/sessions/${id}/run`, {
        method: "POST",
        body: JSON.stringify({
          command,
          args: args.split(/\s+/).filter(Boolean),
          timeout_seconds: 30,
        }),
      });
      setRunResult(data);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setRunning(false);
    }
  }

  async function uploadFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const formData = new FormData();
      formData.append("file", file);
      formData.append("path", file.name);
      const res = await fetch(`/api/proxy/api/v1/sessions/${id}/files`, {
        method: "POST",
        body: formData,
      });
      if (!res.ok) throw new Error((await res.json()).error ?? res.statusText);
      await loadFiles();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }

  async function downloadFile(path: string) {
    const clean = path.replace(/^\//, "");
    const res = await fetch(`/api/proxy/api/v1/sessions/${id}/files/${clean}`);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = path.split("/").pop() ?? "file";
    a.click();
    URL.revokeObjectURL(url);
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 p-8 text-slate-500 text-sm">
        <Loader2 className="w-4 h-4 animate-spin" />
        Loading session…
      </div>
    );
  }

  if (!session) {
    return (
      <div className="p-8 text-red-400 text-sm flex items-center gap-2">
        <AlertCircle className="w-4 h-4" />
        Session not found.
      </div>
    );
  }

  return (
    <div className="p-8">
      {/* Header */}
      <div className="flex items-start gap-4 mb-6">
        <button
          onClick={() => router.push("/dashboard")}
          className="mt-0.5 p-1.5 rounded text-slate-600 hover:text-slate-300 transition-colors"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        <div className="flex-1">
          <h1 className="text-lg font-semibold text-slate-100">
            {session.name ?? session.id.slice(0, 16)}
          </h1>
          <div className="flex items-center gap-3 mt-1 text-xs text-slate-500">
            <span className="font-mono">{session.id}</span>
            <span>·</span>
            <span className="font-mono">{session.image}</span>
            <span>·</span>
            <span className={`px-2 py-0.5 rounded-full border ${
              session.status === "running" ? "bg-green-500/20 text-green-400 border-green-500/30" : "bg-slate-700/40 text-slate-400 border-slate-600/30"
            }`}>{session.status}</span>
            <span>·</span>
            <span>{session.cpu_limit} CPU · {session.memory_limit_mb} MB</span>
          </div>
        </div>
      </div>

      {/* Error banner */}
      {error && (
        <div className="mb-4 flex items-center gap-2 px-4 py-3 rounded-lg bg-red-900/20 border border-red-700/40 text-red-400 text-sm">
          <AlertCircle className="w-4 h-4 shrink-0" />
          {error}
          <button onClick={() => setError("")} className="ml-auto text-red-600 hover:text-red-400">✕</button>
        </div>
      )}

      {/* Tabs */}
      <div className="flex gap-1 border-b border-slate-800 mb-6">
        {TABS.map(t => (
          <button
            key={t}
            onClick={() => handleTabChange(t)}
            className={`flex items-center gap-1.5 px-4 py-2.5 text-sm border-b-2 transition-colors ${
              tab === t
                ? "border-indigo-500 text-indigo-300"
                : "border-transparent text-slate-500 hover:text-slate-300"
            }`}
          >
            {t === "Run" && <Terminal className="w-3.5 h-3.5" />}
            {t === "Files" && <FolderOpen className="w-3.5 h-3.5" />}
            {t === "Runs" && <Play className="w-3.5 h-3.5" />}
            {t === "Audit" && <ScrollText className="w-3.5 h-3.5" />}
            {t}
          </button>
        ))}
      </div>

      {/* ── Run tab ── */}
      {tab === "Run" && (
        <div className="space-y-4">
          <div className="flex gap-3">
            <div className="flex-1">
              <label className="block text-xs text-slate-500 mb-1.5">Command</label>
              <input
                value={command}
                onChange={e => setCommand(e.target.value)}
                placeholder="python"
                onKeyDown={e => e.key === "Enter" && runCommand()}
                className="w-full bg-[#0f0f1a] border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 placeholder-slate-600 focus:outline-none focus:border-indigo-600"
              />
            </div>
            <div className="flex-[2]">
              <label className="block text-xs text-slate-500 mb-1.5">Arguments</label>
              <input
                value={args}
                onChange={e => setArgs(e.target.value)}
                placeholder="script.py --arg value"
                onKeyDown={e => e.key === "Enter" && runCommand()}
                className="w-full bg-[#0f0f1a] border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 placeholder-slate-600 focus:outline-none focus:border-indigo-600"
              />
            </div>
            <div className="flex items-end">
              <button
                onClick={runCommand}
                disabled={running || !command}
                className="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white text-sm font-medium transition-colors"
              >
                {running ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Play className="w-3.5 h-3.5" />}
                Run
              </button>
            </div>
          </div>

          {runResult && (
            <div className="bg-[#07070d] border border-slate-800 rounded-xl p-5 space-y-3">
              <div className="flex items-center gap-3 text-xs text-slate-500">
                <span className={`px-2 py-0.5 rounded-full border ${
                  runResult.exit_code === 0
                    ? "bg-green-500/20 text-green-400 border-green-500/30"
                    : "bg-red-500/20 text-red-400 border-red-500/30"
                }`}>
                  exit {runResult.exit_code ?? "?"}
                </span>
                <span>{runResult.status}</span>
                {runResult.duration_ms != null && <span>{runResult.duration_ms}ms</span>}
              </div>
              {runResult.stdout && (
                <div>
                  <div className="text-xs text-slate-600 mb-1">stdout</div>
                  <pre className="text-xs font-mono text-slate-300 whitespace-pre-wrap break-all max-h-64 overflow-y-auto">
                    {runResult.stdout}
                  </pre>
                </div>
              )}
              {runResult.stderr && (
                <div>
                  <div className="text-xs text-slate-600 mb-1">stderr</div>
                  <pre className="text-xs font-mono text-red-400/80 whitespace-pre-wrap break-all max-h-32 overflow-y-auto">
                    {runResult.stderr}
                  </pre>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* ── Files tab ── */}
      {tab === "Files" && (
        <div>
          <div className="flex items-center justify-between mb-4">
            <p className="text-sm text-slate-500">{files.length} file(s)</p>
            <div className="flex gap-2">
              <button onClick={loadFiles} className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 text-xs transition-colors">
                <RefreshCw className="w-3 h-3" /> Refresh
              </button>
              <label className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-medium cursor-pointer transition-colors">
                {uploading ? <Loader2 className="w-3 h-3 animate-spin" /> : <Upload className="w-3 h-3" />}
                Upload
                <input ref={fileInputRef} type="file" className="hidden" onChange={uploadFile} />
              </label>
            </div>
          </div>

          {filesLoading ? (
            <div className="flex items-center gap-2 text-slate-500 text-sm"><Loader2 className="w-4 h-4 animate-spin" /> Loading…</div>
          ) : files.length === 0 ? (
            <div className="text-center py-12 text-slate-600 text-sm">No files in workspace.</div>
          ) : (
            <div className="bg-[#0f0f1a] border border-slate-800 rounded-xl overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-slate-800 text-xs text-slate-500">
                    <th className="text-left px-5 py-3 font-medium">Path</th>
                    <th className="text-left px-5 py-3 font-medium">Size</th>
                    <th className="text-left px-5 py-3 font-medium">Type</th>
                    <th className="px-5 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60">
                  {files.map(f => (
                    <tr key={f.id} className="hover:bg-slate-800/20 transition-colors">
                      <td className="px-5 py-2.5 font-mono text-xs text-slate-300">{f.path}</td>
                      <td className="px-5 py-2.5 text-xs text-slate-500">{(f.size_bytes / 1024).toFixed(1)} KB</td>
                      <td className="px-5 py-2.5 text-xs text-slate-600">{f.content_type}</td>
                      <td className="px-5 py-2.5">
                        <button onClick={() => downloadFile(f.path)} className="p-1.5 rounded text-slate-600 hover:text-indigo-400 transition-colors">
                          <Download className="w-3.5 h-3.5" />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* ── Runs tab ── */}
      {tab === "Runs" && (
        <div>
          <div className="flex items-center justify-between mb-4">
            <p className="text-sm text-slate-500">{runs.length} run(s)</p>
            <button onClick={loadRuns} className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 text-xs transition-colors">
              <RefreshCw className="w-3 h-3" /> Refresh
            </button>
          </div>
          {runsLoading ? (
            <div className="flex items-center gap-2 text-slate-500 text-sm"><Loader2 className="w-4 h-4 animate-spin" /> Loading…</div>
          ) : runs.length === 0 ? (
            <div className="text-center py-12 text-slate-600 text-sm">No runs yet. Use the Run tab to execute a command.</div>
          ) : (
            <div className="space-y-2">
              {runs.map(r => (
                <div key={r.id} className="bg-[#0f0f1a] border border-slate-800 rounded-xl overflow-hidden">
                  <button
                    onClick={() => setExpandedRun(expandedRun === r.id ? null : r.id)}
                    className="w-full flex items-center gap-3 px-5 py-3 text-left hover:bg-slate-800/20 transition-colors"
                  >
                    <span className={`px-2 py-0.5 rounded-full border text-xs ${
                      r.exit_code === 0 ? "bg-green-500/20 text-green-400 border-green-500/30" : "bg-red-500/20 text-red-400 border-red-500/30"
                    }`}>
                      {r.exit_code ?? "?"}
                    </span>
                    <span className="font-mono text-xs text-slate-300 flex-1">
                      {r.command} {r.args?.join(" ")}
                    </span>
                    {r.duration_ms != null && <span className="text-xs text-slate-600">{r.duration_ms}ms</span>}
                    <span className="text-xs text-slate-600">{new Date(r.created_at).toLocaleTimeString()}</span>
                    <ChevronRight className={`w-3.5 h-3.5 text-slate-600 transition-transform ${expandedRun === r.id ? "rotate-90" : ""}`} />
                  </button>
                  {expandedRun === r.id && (r.stdout || r.stderr) && (
                    <div className="border-t border-slate-800 px-5 py-4 space-y-3 bg-[#07070d]">
                      {r.stdout && (
                        <pre className="text-xs font-mono text-slate-300 whitespace-pre-wrap break-all max-h-48 overflow-y-auto">{r.stdout}</pre>
                      )}
                      {r.stderr && (
                        <pre className="text-xs font-mono text-red-400/80 whitespace-pre-wrap break-all max-h-32 overflow-y-auto">{r.stderr}</pre>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* ── Audit tab ── */}
      {tab === "Audit" && (
        <div>
          <div className="flex items-center justify-between mb-4">
            <p className="text-sm text-slate-500">{auditLogs.length} event(s)</p>
            <button onClick={loadAudit} className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 text-xs transition-colors">
              <RefreshCw className="w-3 h-3" /> Refresh
            </button>
          </div>
          {auditLoading ? (
            <div className="flex items-center gap-2 text-slate-500 text-sm"><Loader2 className="w-4 h-4 animate-spin" /> Loading…</div>
          ) : auditLogs.length === 0 ? (
            <div className="text-center py-12 text-slate-600 text-sm">No audit events for this session.</div>
          ) : (
            <div className="bg-[#0f0f1a] border border-slate-800 rounded-xl overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-slate-800 text-xs text-slate-500">
                    <th className="text-left px-5 py-3 font-medium">Time</th>
                    <th className="text-left px-5 py-3 font-medium">Actor</th>
                    <th className="text-left px-5 py-3 font-medium">Action</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-800/60">
                  {auditLogs.map(log => (
                    <tr key={log.id} className="hover:bg-slate-800/20 transition-colors">
                      <td className="px-5 py-2.5 text-xs text-slate-600 font-mono">
                        {new Date(log.timestamp).toLocaleString()}
                      </td>
                      <td className="px-5 py-2.5 text-xs text-slate-400">{log.actor}</td>
                      <td className="px-5 py-2.5 text-xs">
                        <span className="px-2 py-0.5 rounded bg-slate-800 text-slate-300 font-mono">{log.action}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
