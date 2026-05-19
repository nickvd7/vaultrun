"use client";

import { useEffect, useState, useRef } from "react";
import { useParams, useRouter } from "next/navigation";
import { ArrowLeft, Play, Upload, FileText, ScrollText, Trash2 } from "lucide-react";
import Link from "next/link";
import { api } from "@/lib/api";
import { StatusBadge } from "@/components/StatusBadge";
import { formatBytes, formatDuration, formatDate } from "@/lib/utils";
import type { Session, Run, File } from "@/types";

type Tab = "runs" | "files" | "audit";

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();

  const [session, setSession] = useState<Session | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [files, setFiles] = useState<File[]>([]);
  const [tab, setTab] = useState<Tab>("runs");
  const [selectedRun, setSelectedRun] = useState<Run | null>(null);

  // Run form
  const [cmd, setCmd] = useState("python");
  const [args, setArgs] = useState("");
  const [runTimeout, setRunTimeout] = useState("30");
  const [executing, setExecuting] = useState(false);

  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    api.sessions.get(id).then(setSession).catch(console.error);
    api.runs.list(id).then(setRuns).catch(console.error);
    api.files.list(id).then(setFiles).catch(console.error);
  }, [id]);

  const handleRun = async () => {
    if (!cmd) return;
    setExecuting(true);
    try {
      const result = await api.runs.execute(id, {
        command: cmd,
        args: args
          .split(" ")
          .map((a) => a.trim())
          .filter(Boolean),
        timeout_seconds: parseInt(runTimeout) || 30,
      });
      setRuns((prev) => [result, ...prev]);
      setSelectedRun(result);
      setTab("runs");
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Run failed");
    } finally {
      setExecuting(false);
    }
  };

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      const uploaded = await api.files.upload(id, file.name, file);
      setFiles((prev) => {
        const idx = prev.findIndex((f) => f.path === uploaded.path);
        if (idx >= 0) {
          const next = [...prev];
          next[idx] = uploaded;
          return next;
        }
        return [...prev, uploaded];
      });
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Upload failed");
    }
    if (fileRef.current) fileRef.current.value = "";
  };

  const handleDelete = async () => {
    if (!confirm("Delete this session and all its data?")) return;
    await api.sessions.delete(id).catch(console.error);
    router.push("/sessions");
  };

  if (!session) {
    return (
      <div className="text-slate-600 text-sm p-8">Loading session…</div>
    );
  }

  return (
    <div className="max-w-5xl mx-auto space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <Link
            href="/sessions"
            className="text-slate-500 hover:text-slate-300"
          >
            <ArrowLeft className="w-4 h-4" />
          </Link>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold text-slate-100 font-mono">
                {session.name || session.id.slice(0, 8)}
              </h1>
              <StatusBadge status={session.status} />
            </div>
            <p className="text-xs text-slate-500 mt-0.5 font-mono">{session.id}</p>
          </div>
        </div>
        <button
          onClick={handleDelete}
          className="flex items-center gap-1.5 px-3 py-2 text-xs text-red-400 border border-red-900/50 rounded hover:bg-red-900/20"
        >
          <Trash2 className="w-3.5 h-3.5" /> Delete
        </button>
      </div>

      {/* Session info */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {[
          { label: "Image", value: session.image },
          { label: "CPU", value: `${session.cpu_limit} core(s)` },
          { label: "Memory", value: `${session.memory_limit_mb} MB` },
          {
            label: "Network",
            value: session.network_enabled ? "Enabled" : "Disabled",
          },
        ].map(({ label, value }) => (
          <div
            key={label}
            className="bg-[#0f0f1a] border border-slate-800 rounded-md px-4 py-3"
          >
            <div className="text-xs text-slate-500 mb-1">{label}</div>
            <div className="text-sm text-slate-200 font-mono">{value}</div>
          </div>
        ))}
      </div>

      {/* Execute command */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-4">
        <h2 className="text-xs font-medium text-slate-400 uppercase tracking-wide mb-3">
          Execute Command
        </h2>
        <div className="flex gap-2">
          <input
            className="bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 w-32 focus:outline-none focus:border-indigo-500"
            value={cmd}
            onChange={(e) => setCmd(e.target.value)}
            placeholder="command"
          />
          <input
            className="flex-1 bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
            value={args}
            onChange={(e) => setArgs(e.target.value)}
            placeholder="arguments (space-separated)"
          />
          <input
            type="number"
            className="bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 w-20 focus:outline-none focus:border-indigo-500"
            value={runTimeout}
            onChange={(e) => setRunTimeout(e.target.value)}
            title="Timeout (seconds)"
          />
          <button
            onClick={handleRun}
            disabled={executing || session.status !== "running"}
            className="flex items-center gap-1.5 px-4 py-2 text-sm text-white bg-indigo-600 rounded hover:bg-indigo-500 disabled:opacity-40"
          >
            <Play className="w-3.5 h-3.5" />
            {executing ? "Running…" : "Run"}
          </button>
        </div>
      </div>

      {/* Tabs */}
      <div>
        <div className="flex gap-4 border-b border-slate-800 mb-4">
          {(["runs", "files", "audit"] as Tab[]).map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`pb-2 text-sm capitalize transition-colors ${
                tab === t
                  ? "text-indigo-400 border-b-2 border-indigo-400"
                  : "text-slate-500 hover:text-slate-300"
              }`}
            >
              {t === "runs" && (
                <Play className="w-3.5 h-3.5 inline mr-1" />
              )}
              {t === "files" && (
                <FileText className="w-3.5 h-3.5 inline mr-1" />
              )}
              {t === "audit" && (
                <ScrollText className="w-3.5 h-3.5 inline mr-1" />
              )}
              {t}
            </button>
          ))}
        </div>

        {tab === "runs" && (
          <RunsTab
            runs={runs}
            selectedRun={selectedRun}
            setSelectedRun={setSelectedRun}
          />
        )}
        {tab === "files" && (
          <FilesTab
            sessionId={id}
            files={files}
            fileRef={fileRef}
            handleUpload={handleUpload}
          />
        )}
        {tab === "audit" && <AuditTab sessionId={id} />}
      </div>
    </div>
  );
}

// ── Runs Tab ──────────────────────────────────────────────────────────────────
function RunsTab({
  runs,
  selectedRun,
  setSelectedRun,
}: {
  runs: Run[];
  selectedRun: Run | null;
  setSelectedRun: (r: Run | null) => void;
}) {
  return (
    <div className="space-y-4">
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {runs.length === 0 ? (
          <div className="p-6 text-center text-slate-600 text-sm">
            No runs yet.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">ID</th>
                <th className="px-4 py-3 text-left">Command</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Exit</th>
                <th className="px-4 py-3 text-left">Duration</th>
                <th className="px-4 py-3 text-left">Time</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {runs.map((r) => (
                <tr
                  key={r.id}
                  onClick={() =>
                    setSelectedRun(selectedRun?.id === r.id ? null : r)
                  }
                  className={`cursor-pointer hover:bg-slate-800/20 transition-colors ${
                    selectedRun?.id === r.id ? "bg-slate-800/30" : ""
                  }`}
                >
                  <td className="px-4 py-2.5 font-mono text-xs text-indigo-400">
                    {r.id.slice(0, 8)}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-300">
                    {r.command} {r.args?.join(" ")}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge status={r.status} />
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-400">
                    {r.exit_code ?? "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-500">
                    {r.duration_ms ? formatDuration(r.duration_ms) : "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-500">
                    {formatDate(r.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Log viewer */}
      {selectedRun && (
        <div className="bg-[#060609] border border-slate-800 rounded-lg p-4">
          <div className="flex items-center justify-between mb-3">
            <span className="text-xs text-slate-500 font-mono">
              {selectedRun.command} {selectedRun.args?.join(" ")}
            </span>
            <StatusBadge status={selectedRun.status} />
          </div>
          {selectedRun.stdout && (
            <div className="mb-3">
              <div className="text-xs text-green-600 mb-1 uppercase tracking-wide">
                stdout
              </div>
              <pre className="log-output text-green-300 bg-black/30 rounded p-3 max-h-64 overflow-auto">
                {selectedRun.stdout}
              </pre>
            </div>
          )}
          {selectedRun.stderr && (
            <div>
              <div className="text-xs text-red-600 mb-1 uppercase tracking-wide">
                stderr
              </div>
              <pre className="log-output text-red-300 bg-black/30 rounded p-3 max-h-64 overflow-auto">
                {selectedRun.stderr}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ── Files Tab ─────────────────────────────────────────────────────────────────
function FilesTab({
  sessionId,
  files,
  fileRef,
  handleUpload,
}: {
  sessionId: string;
  files: File[];
  fileRef: React.RefObject<HTMLInputElement | null>;
  handleUpload: (e: React.ChangeEvent<HTMLInputElement>) => void;
}) {
  const handleDownload = async (path: string) => {
    const resp = await api.files.download(sessionId, path.replace(/^\//, ""));
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = path.split("/").pop() || "file";
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <label className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded cursor-pointer hover:bg-slate-800">
          <Upload className="w-3.5 h-3.5" /> Upload File
          <input
            ref={fileRef}
            type="file"
            className="hidden"
            onChange={handleUpload}
          />
        </label>
      </div>

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {files.length === 0 ? (
          <div className="p-6 text-center text-slate-600 text-sm">
            No files uploaded.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Path</th>
                <th className="px-4 py-3 text-left">Size</th>
                <th className="px-4 py-3 text-left">Type</th>
                <th className="px-4 py-3 text-left">Updated</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {files.map((f) => (
                <tr key={f.id} className="hover:bg-slate-800/20">
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-300">
                    {f.path}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-500">
                    {formatBytes(f.size_bytes)}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-500 font-mono">
                    {f.content_type}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-500">
                    {formatDate(f.updated_at)}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    <button
                      onClick={() => handleDownload(f.path)}
                      className="text-xs text-indigo-400 hover:text-indigo-300"
                    >
                      Download
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

// ── Audit Tab ─────────────────────────────────────────────────────────────────
function AuditTab({ sessionId }: { sessionId: string }) {
  const [logs, setLogs] = useState<
    Array<{
      id: string;
      timestamp: string;
      actor: string;
      action: string;
      metadata: Record<string, unknown>;
    }>
  >([]);

  useEffect(() => {
    api.audit.list(sessionId).then(setLogs).catch(console.error);
  }, [sessionId]);

  return (
    <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
      {logs.length === 0 ? (
        <div className="p-6 text-center text-slate-600 text-sm">
          No audit logs.
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
              <th className="px-4 py-3 text-left">Time</th>
              <th className="px-4 py-3 text-left">Actor</th>
              <th className="px-4 py-3 text-left">Action</th>
              <th className="px-4 py-3 text-left">Metadata</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/50">
            {logs.map((l) => (
              <tr key={l.id} className="hover:bg-slate-800/20">
                <td className="px-4 py-2.5 text-xs text-slate-500 font-mono">
                  {formatDate(l.timestamp)}
                </td>
                <td className="px-4 py-2.5 text-xs text-slate-400">
                  {l.actor}
                </td>
                <td className="px-4 py-2.5 font-mono text-xs text-indigo-300">
                  {l.action}
                </td>
                <td className="px-4 py-2.5 text-xs text-slate-500 font-mono truncate max-w-xs">
                  {JSON.stringify(l.metadata)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
