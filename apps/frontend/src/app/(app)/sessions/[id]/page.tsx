"use client";

import { useEffect, useState, useRef, useCallback } from "react";
import { useParams, useRouter } from "next/navigation";
import { ArrowLeft, Play, Zap, Upload, FileText, ScrollText, Trash2, Square, Tag, Plus, X, Check, History, RotateCcw, GitFork } from "lucide-react";
import Link from "next/link";
import { api, getStoredApiKey, ApiError } from "@/lib/api";
import { splitCommandLine, formatArgs, defaultCommandFor, formatCommand } from "@/lib/shellwords";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Notice } from "@/components/ui/Notice";
import { useToast } from "@/components/ui/Toast";
import { StatusBadge } from "@/components/StatusBadge";
import { Time } from "@/components/ui/Time";
import { formatBytes, formatDuration, formatDate } from "@/lib/utils";
import { MetadataInline, SigCell, actorLabel } from "@/components/audit/shared";
import { useApiKey } from "@/components/ApiKeyGate";
import type { Session, Run, File, Checkpoint, AuditLog } from "@/types";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "";

type Tab = "runs" | "files" | "checkpoints" | "audit";

// Real capitalised text: CSS text-transform changes how the tab looks, not what a screen reader announces.
const TAB_LABELS: Record<Tab, string> = { runs: "Runs", files: "Files", checkpoints: "Checkpoints", audit: "Audit" };

export default function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const toast = useToast();

  const [session, setSession] = useState<Session | null>(null);
  const [loadState, setLoadState] = useState<"loading" | "ready" | "notfound" | "error">("loading");
  const [loadError, setLoadError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [runs, setRuns] = useState<Run[]>([]);
  const [files, setFiles] = useState<File[]>([]);
  const [checkpoints, setCheckpoints] = useState<Checkpoint[]>([]);
  const [tab, setTab] = useState<Tab>("runs");
  const [selectedRun, setSelectedRun] = useState<Run | null>(null);

  // Run form
  const [cmd, setCmd] = useState("");
  const [args, setArgs] = useState("");
  const [argsError, setArgsError] = useState("");
  const [runError, setRunError] = useState("");
  const [cmdTouched, setCmdTouched] = useState(false);
  const [argsTouched, setArgsTouched] = useState(false);
  const [runTimeout, setRunTimeout] = useState("30");
  const [executing, setExecuting] = useState(false);
  const [streaming, setStreaming] = useState(false);
  const [asyncRunId, setAsyncRunId] = useState<string | null>(null);
  const [asyncPolling, setAsyncPolling] = useState(false);

  // Labels editor
  const [editingLabels, setEditingLabels] = useState(false);
  const [pendingLabels, setPendingLabels] = useState<Record<string, string>>({});
  const [newLabelKey, setNewLabelKey] = useState("");
  const [newLabelVal, setNewLabelVal] = useState("");

  // Live stream output
  const [streamOutput, setStreamOutput] = useState<{ kind: "out" | "err"; text: string }[]>([]);
  const streamAbortRef = useRef<AbortController | null>(null);
  const streamEndRef = useRef<HTMLDivElement | null>(null);

  const fileRef = useRef<HTMLInputElement>(null);

  const loadSession = useCallback(() => {
    setLoadState("loading");
    api.sessions
      .get(id)
      .then((s) => {
        setSession(s);
        // Stopping is final and stopped sessions drop out of every list, so a
        // stopped session is "gone" here too; the API keeps the row for history.
        setLoadState(s.stopped_at ? "notfound" : "ready");
      })
      .catch((e: unknown) => {
        // 400 = malformed id, 403/404 = not yours or gone: all look like "not found" to the user.
        if (e instanceof ApiError && [400, 403, 404].includes(e.status)) {
          setLoadState("notfound");
        } else {
          setLoadError(e instanceof Error ? e.message : "Failed to load session");
          setLoadState("error");
        }
      });
  }, [id]);

  const loadRuns = useCallback(() => {
    api.runs.list(id).then(setRuns).catch(console.error);
  }, [id]);

  const loadCheckpoints = useCallback(() => {
    api.checkpoints.list(id).then(setCheckpoints).catch(console.error);
  }, [id]);

  useEffect(() => {
    loadSession();
  }, [id, loadSession]);

  // Child resources are only useful once the session itself loaded. A missing
  // or forbidden id would otherwise produce a run of 404s.
  useEffect(() => {
    if (loadState !== "ready" || session?.id !== id) return;
    loadRuns();
    loadCheckpoints();
    api.files.list(id).then(setFiles).catch(console.error);
  }, [id, loadState, session?.id, loadRuns, loadCheckpoints]);

  // Preselect a command that exists in the session's image (python is not in alpine).
  useEffect(() => {
    if (!session || cmdTouched) return;
    const d = defaultCommandFor(session.image);
    setCmd(d.command);
    setArgs(d.args);
  }, [session, cmdTouched]);

  // Auto-scroll stream output
  useEffect(() => {
    streamEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [streamOutput]);

  // Returns null (and shows the problem) when the arguments cannot be parsed.
  const buildRunBody = () => {
    let parsed: { command: string; args: string[] };
    try {
      parsed = splitCommandLine(cmd, args);
    } catch (e) {
      setArgsError(e instanceof Error ? e.message : "Invalid arguments");
      return null;
    }
    setArgsError("");
    setRunError("");
    if (parsed.command !== cmd) {
      setCmd(parsed.command);
      setArgs(formatArgs(parsed.args));
    }
    return {
      command: parsed.command,
      args: parsed.args,
      timeout_seconds: parseInt(runTimeout) || 30,
    };
  };

  // Blocking run — waits for full output
  const handleRun = async () => {
    const body = buildRunBody();
    if (!cmd.trim() || !body) return;
    setExecuting(true);
    try {
      const result = await api.runs.execute(id, body);
      setRuns((prev) => [result, ...prev]);
      setSelectedRun(result);
      setTab("runs");
    } catch (e: unknown) {
      setRunError(e instanceof Error ? e.message : "Run failed");
    } finally {
      setExecuting(false);
    }
  };

  // Streaming run — live SSE output
  const handleStream = async () => {
    const body = buildRunBody();
    if (!cmd.trim() || !body) return;
    setStreaming(true);
    setStreamOutput([]);
    setSelectedRun(null);

    const abort = new AbortController();
    streamAbortRef.current = abort;

    try {
      const resp = await fetch(`${API_BASE}/api/v1/sessions/${id}/run/stream`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-API-Key": getStoredApiKey(),
          "Accept": "text/event-stream",
        },
        body: JSON.stringify(body),
        signal: abort.signal,
      });

      if (!resp.ok || !resp.body) {
        const msg = await resp.json().catch(() => ({ error: resp.statusText }));
        throw new Error(msg.error || `HTTP ${resp.status}`);
      }

      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });

        const lines = buf.split("\n");
        buf = lines.pop() ?? "";

        for (const line of lines) {
          if (!line.startsWith("data: ")) continue;
          try {
            const ev: { type: string; data?: string; status?: string; exit_code?: number } =
              JSON.parse(line.slice(6));
            if (ev.type === "stdout" && ev.data) {
              setStreamOutput((p) => [...p, { kind: "out", text: ev.data! }]);
            } else if (ev.type === "stderr" && ev.data) {
              setStreamOutput((p) => [...p, { kind: "err", text: ev.data! }]);
            } else if (ev.type === "done") {
              loadRuns(); // refresh run list to pick up the new record
            }
          } catch { /* ignore malformed events */ }
        }
      }
    } catch (e: unknown) {
      if ((e as Error).name !== "AbortError") {
        setStreamOutput((p) => [...p, { kind: "err", text: `\n[error: ${(e as Error).message}]` }]);
      }
    } finally {
      setStreaming(false);
      streamAbortRef.current = null;
    }
  };

  const handleStopStream = () => {
    streamAbortRef.current?.abort();
  };

  // Async run — submit and poll until completion
  const handleAsync = async () => {
    const body = buildRunBody();
    if (!cmd.trim() || !body) return;
    setAsyncPolling(true);
    setAsyncRunId(null);
    try {
      const { run_id } = await api.runs.submitAsync(id, body);
      setAsyncRunId(run_id);
      const result = await api.runs.poll(run_id);
      setRuns((prev) => [result, ...prev]);
      setSelectedRun(result);
      setTab("runs");
    } catch (e: unknown) {
      setRunError(e instanceof Error ? e.message : "Async run failed");
    } finally {
      setAsyncPolling(false);
      setAsyncRunId(null);
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
      toast.error(e instanceof Error ? e.message : "Upload failed");
    }
    if (fileRef.current) fileRef.current.value = "";
  };

  const handleDelete = async () => {
    setDeleting(true);
    try {
      await api.sessions.delete(id);
      toast.success(`Session ${session?.name || id.slice(0, 8)} deleted`);
      router.push("/sessions");
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to delete session");
      setDeleting(false);
      setConfirmDelete(false);
    }
  };

  const handleEditLabels = () => {
    setPendingLabels({ ...(session?.labels ?? {}) });
    setNewLabelKey("");
    setNewLabelVal("");
    setEditingLabels(true);
  };

  const handleSaveLabels = async () => {
    try {
      await api.sessions.updateLabels(id, pendingLabels);
      setSession((s) => s ? { ...s, labels: pendingLabels } : s);
      setEditingLabels(false);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to save labels");
    }
  };

  const handleAddPendingLabel = () => {
    const k = newLabelKey.trim();
    const v = newLabelVal.trim();
    if (!k) return;
    setPendingLabels((p) => ({ ...p, [k]: v }));
    setNewLabelKey("");
    setNewLabelVal("");
  };

  const handleRemovePendingLabel = (key: string) => {
    setPendingLabels((p) => {
      const next = { ...p };
      delete next[key];
      return next;
    });
  };

  if (loadState === "loading" && !session) {
    return <div className="text-slate-300 text-sm p-8" role="status">Loading session…</div>;
  }

  if (loadState === "notfound") {
    return (
      <div className="space-y-4 py-8">
        <h1 className="text-2xl font-semibold text-slate-100">Session not found</h1>
        {session?.stopped_at ? (
          <p className="text-sm text-slate-300">
            The session <span className="font-mono text-slate-200">{session.name || id}</span> no longer exists: it was deleted
            or stopped when idle on <Time value={session.stopped_at} absolute />. Its events remain in the{" "}
            <Link href="/audit" className="text-indigo-300 underline">audit log</Link>.
          </p>
        ) : (
          <p className="text-sm text-slate-300">
            There is no session with ID <span className="font-mono text-slate-300">{id}</span>, or you do not have access to it. It may have been deleted.
          </p>
        )}
        <Link href="/sessions" className="inline-flex items-center gap-1.5 text-sm text-indigo-300 hover:underline">
          <ArrowLeft className="w-4 h-4" aria-hidden="true" /> Back to sessions
        </Link>
      </div>
    );
  }

  if (loadState === "error" || !session) {
    return (
      <div className="space-y-4 py-8">
        <Notice tone="error" title="Could not load session">{loadError || "Unknown error"}</Notice>
        <button onClick={loadSession} className="px-4 py-2 rounded-lg border border-slate-700 text-sm text-slate-200 hover:bg-slate-800">
          Try again
        </button>
      </div>
    );
  }

  const canRun = session.status === "running" && !executing && !streaming && !asyncPolling;

  return (
    <div className="space-y-6">
      <ConfirmDialog
        open={confirmDelete}
        title="Delete session?"
        message={
          <>
            <span className="font-mono">{session.name || session.id.slice(0, 8)}</span> and all of its files, runs and snapshots will be removed. This cannot be undone.
          </>
        }
        confirmLabel="Delete session"
        busyLabel="Deleting…"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setConfirmDelete(false)}
      />
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <Link href="/sessions" aria-label="Back to sessions" className="text-slate-300 hover:text-slate-200">
            <ArrowLeft className="w-4 h-4" aria-hidden="true" />
          </Link>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold text-slate-100 font-mono">
                {session.name || session.id.slice(0, 8)}
              </h1>
              <StatusBadge status={session.status} />
            </div>
            <p className="text-xs text-slate-200 mt-0.5 font-mono">{session.id}</p>
          </div>
        </div>
        <button
          onClick={() => setConfirmDelete(true)}
          className="flex items-center gap-1.5 px-3 py-2 text-xs text-red-300 border border-red-900/50 rounded hover:bg-red-900/20"
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
          { label: "Network", value: session.network_enabled ? "Enabled" : "Disabled" },
        ].map(({ label, value }) => (
          <div key={label} className="bg-[#0f0f1a] border border-slate-800 rounded-md px-4 py-3">
            <div className="text-xs text-slate-200 mb-1">{label}</div>
            <div className="text-sm text-slate-200 font-mono">{value}</div>
          </div>
        ))}
      </div>

      {/* Labels panel */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-4 space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-xs font-medium text-slate-200 uppercase tracking-wide flex items-center gap-1.5">
            <Tag className="w-3.5 h-3.5" /> Labels
          </h2>
          {!editingLabels && (
            <button
              onClick={handleEditLabels}
              className="text-xs text-slate-200 hover:text-slate-200 border border-slate-700 rounded px-2 py-1"
            >
              Edit
            </button>
          )}
        </div>

        {!editingLabels ? (
          /* View mode */
          <div className="flex flex-wrap gap-2 min-h-[1.5rem]">
            {Object.keys(session.labels ?? {}).length === 0 ? (
              <span className="text-xs text-slate-200 italic">No labels</span>
            ) : (
              Object.entries(session.labels ?? {}).map(([k, v]) => (
                <span
                  key={k}
                  className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-slate-800 text-xs font-mono text-slate-200"
                >
                  <span className="text-indigo-400">{k}</span>
                  <span className="text-slate-300">=</span>
                  {v}
                </span>
              ))
            )}
          </div>
        ) : (
          /* Edit mode */
          <div className="space-y-2">
            {/* Existing labels */}
            {Object.entries(pendingLabels).map(([k, v]) => (
              <div key={k} className="flex items-center gap-2">
                <input
                  className="bg-slate-900 border border-slate-700 rounded px-2 py-1 text-xs font-mono text-indigo-200 w-36 focus:outline-none focus:border-indigo-500"
                  value={k}
                  readOnly
                />
                <span className="text-slate-200 text-xs">=</span>
                <input
                  className="flex-1 bg-slate-900 border border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
                  value={v}
                  onChange={(e) => setPendingLabels((p) => ({ ...p, [k]: e.target.value }))}
                />
                <button
                  onClick={() => handleRemovePendingLabel(k)}
                  className="text-red-400 hover:text-red-300"
                  aria-label={`Remove label ${k}`}
                  title="Remove label"
                >
                  <X className="w-3.5 h-3.5" aria-hidden="true" />
                </button>
              </div>
            ))}

            {/* Add new label row */}
            <div className="flex items-center gap-2 pt-1 border-t border-slate-800">
              <input
                className="bg-slate-900 border border-slate-700 rounded px-2 py-1 text-xs font-mono text-indigo-200 w-36 focus:outline-none focus:border-indigo-500"
                placeholder="key"
                value={newLabelKey}
                onChange={(e) => setNewLabelKey(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleAddPendingLabel()}
              />
              <span className="text-slate-200 text-xs">=</span>
              <input
                className="flex-1 bg-slate-900 border border-slate-700 rounded px-2 py-1 text-xs font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
                placeholder="value"
                value={newLabelVal}
                onChange={(e) => setNewLabelVal(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleAddPendingLabel()}
              />
              <button
                onClick={handleAddPendingLabel}
                className="text-green-400 hover:text-green-300"
                aria-label="Add label"
                title="Add label"
              >
                <Plus className="w-3.5 h-3.5" aria-hidden="true" />
              </button>
            </div>

            {/* Save / Cancel */}
            <div className="flex gap-2 pt-1">
              <button
                onClick={handleSaveLabels}
                className="flex items-center gap-1 px-3 py-1.5 text-xs text-white bg-indigo-600 rounded hover:bg-indigo-500"
              >
                <Check className="w-3 h-3" /> Save
              </button>
              <button
                onClick={() => setEditingLabels(false)}
                className="px-3 py-1.5 text-xs text-slate-200 border border-slate-700 rounded hover:bg-slate-800"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Execute panel */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-4 space-y-3">
        <h2 className="text-xs font-medium text-slate-200 uppercase tracking-wide">
          Execute Command
        </h2>
        <div className="flex gap-2">
          <input
            className="bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 w-40 sm:w-56 lg:w-72 focus:outline-none focus:border-indigo-500"
            value={cmd}
            onChange={(e) => {
              setCmd(e.target.value);
              setCmdTouched(true);
              // The suggested arguments belong to the suggested command; drop them
              // as soon as the user types their own command or whole command line.
              if (!argsTouched) setArgs("");
            }}
            aria-label="Command"
            placeholder="command"
          />
          <input
            className="flex-1 bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
            value={args}
            onChange={(e) => {
              setArgs(e.target.value);
              setCmdTouched(true);
              setArgsTouched(true);
              setArgsError("");
            }}
            aria-label="Arguments"
            aria-invalid={!!argsError}
            placeholder={'arguments, shell-style quoting: -c "print(2+2)"'}
          />
          <input
            type="number"
            className="bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 w-20 focus:outline-none focus:border-indigo-500"
            value={runTimeout}
            onChange={(e) => setRunTimeout(e.target.value)}
            aria-label="Timeout in seconds"
            title="Timeout (seconds)"
          />
          <button
            onClick={handleRun}
            disabled={!canRun}
            className="flex items-center gap-1.5 px-4 py-2 text-sm text-white bg-indigo-600 rounded hover:bg-indigo-500 disabled:opacity-60"
            title="Run and wait for full output"
          >
            <Play className="w-3.5 h-3.5" />
            {executing ? "Running…" : "Run"}
          </button>
          {streaming ? (
            <button
              onClick={handleStopStream}
              className="flex items-center gap-1.5 px-4 py-2 text-sm text-white bg-red-700 rounded hover:bg-red-600"
            >
              <Square className="w-3.5 h-3.5" /> Stop
            </button>
          ) : (
            <button
              onClick={handleStream}
              disabled={!canRun}
              className="flex items-center gap-1.5 px-4 py-2 text-sm text-white bg-emerald-700 rounded hover:bg-emerald-600 disabled:opacity-60"
              title="Stream live output"
            >
              <Zap className="w-3.5 h-3.5" />
              Stream
            </button>
          )}
          <button
            onClick={handleAsync}
            disabled={!canRun}
            className="flex items-center gap-1.5 px-4 py-2 text-sm text-white bg-amber-700 rounded hover:bg-amber-600 disabled:opacity-60"
            title="Submit async — returns immediately, polls for result"
          >
            <Zap className="w-3 h-3 opacity-60" />
            {asyncPolling ? "Polling…" : "Async"}
          </button>
        </div>

        {(argsError || runError) && (
          <p role="alert" className="text-xs text-red-300">
            {argsError || runError}
          </p>
        )}

        {/* Async run status */}
        {asyncPolling && asyncRunId && (
          <div className="text-xs text-amber-300 font-mono">
            ⏳ Polling run <span>{asyncRunId.slice(0, 8)}…</span>
          </div>
        )}

        {/* Live stream terminal */}
        {(streaming || streamOutput.length > 0) && (
          <div className="bg-black/60 border border-slate-700 rounded-md p-3 max-h-72 overflow-auto font-mono text-xs leading-relaxed">
            {streamOutput.map((line, i) => (
              <span key={i} className={line.kind === "err" ? "text-red-400" : "text-green-300"}>
                {line.text}
              </span>
            ))}
            {streaming && (
              <span className="inline-block w-2 h-3 bg-green-400 animate-pulse ml-0.5" />
            )}
            <div ref={streamEndRef} />
          </div>
        )}
      </div>

      {/* Tabs */}
      <div>
        <div role="tablist" aria-label="Session details" className="flex gap-4 border-b border-slate-800 mb-4">
          {(["runs", "files", "checkpoints", "audit"] as Tab[]).map((t) => (
            <button
              key={t}
              role="tab"
              aria-selected={tab === t}
              onClick={() => setTab(t)}
              className={`pb-2 text-sm transition-colors ${
                tab === t
                  ? "text-indigo-400 border-b-2 border-indigo-400"
                  : "text-slate-300 hover:text-white"
              }`}
            >
              {t === "runs" && <Play className="w-3.5 h-3.5 inline mr-1" aria-hidden="true" />}
              {t === "files" && <FileText className="w-3.5 h-3.5 inline mr-1" aria-hidden="true" />}
              {t === "checkpoints" && <History className="w-3.5 h-3.5 inline mr-1" aria-hidden="true" />}
              {t === "audit" && <ScrollText className="w-3.5 h-3.5 inline mr-1" aria-hidden="true" />}
              {TAB_LABELS[t]}
            </button>
          ))}
        </div>

        {tab === "runs" && (
          <RunsTab runs={runs} selectedRun={selectedRun} setSelectedRun={setSelectedRun} />
        )}
        {tab === "files" && (
          <FilesTab
            sessionId={id}
            files={files}
            fileRef={fileRef}
            handleUpload={handleUpload}
          />
        )}
        {tab === "checkpoints" && (
          <CheckpointsTab
            sessionId={id}
            checkpoints={checkpoints}
            onReload={loadCheckpoints}
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
          <div className="p-6 text-center text-slate-300 text-sm">No runs yet.</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
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
                  onClick={() => setSelectedRun(selectedRun?.id === r.id ? null : r)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      setSelectedRun(selectedRun?.id === r.id ? null : r);
                    }
                  }}
                  tabIndex={0}
                  aria-selected={selectedRun?.id === r.id}
                  className={`cursor-pointer hover:bg-slate-800/20 transition-colors ${
                    selectedRun?.id === r.id ? "bg-slate-800/30" : ""
                  }`}
                >
                  <td className="px-4 py-2.5 font-mono text-xs text-indigo-300">
                    {r.id.slice(0, 8)}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-200">
                    {formatCommand(r.command, r.args)}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge status={r.status} />
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-200">
                    {r.exit_code ?? "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200">
                    {r.duration_ms ? formatDuration(r.duration_ms) : "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200">
                    {formatDate(r.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {selectedRun && (
        <div className="bg-[#060609] border border-slate-800 rounded-lg p-4">
          <div className="flex items-center justify-between mb-3">
            <span className="text-xs text-slate-200 font-mono">
              {formatCommand(selectedRun.command, selectedRun.args)}
            </span>
            <StatusBadge status={selectedRun.status} />
          </div>
          {selectedRun.stdout && (
            <div className="mb-3">
              <div className="text-xs text-green-400 mb-1 uppercase tracking-wide">stdout</div>
              <pre className="text-green-300 bg-black/30 rounded p-3 max-h-64 overflow-auto text-xs font-mono whitespace-pre-wrap">
                {selectedRun.stdout}
              </pre>
            </div>
          )}
          {!selectedRun.stdout && !selectedRun.stderr && (
            <p className="text-xs text-slate-200">
              No output (exit code {selectedRun.exit_code ?? "unknown"}).
            </p>
          )}
          {selectedRun.stderr && (
            <div>
              <div className="text-xs text-red-300 mb-1 uppercase tracking-wide">stderr</div>
              <pre className="text-red-300 bg-black/30 rounded p-3 max-h-64 overflow-auto text-xs font-mono whitespace-pre-wrap">
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
          <input ref={fileRef} type="file" className="hidden" onChange={handleUpload} />
        </label>
      </div>
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {files.length === 0 ? (
          <div className="p-6 text-center text-slate-300 text-sm">No files uploaded.</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
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
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-200">
                    {f.path}
                    {f.symlink && (
                      <span className="ml-2 rounded border border-amber-800/60 bg-amber-950/40 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-amber-200">
                        symlink
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200">{f.symlink ? "—" : formatBytes(f.size_bytes)}</td>
                  <td className="px-4 py-2.5 text-xs text-slate-200 font-mono">{f.symlink ? "symlink" : f.content_type}</td>
                  <td className="px-4 py-2.5 text-xs text-slate-200">{formatDate(f.updated_at)}</td>
                  <td className="px-4 py-2.5 text-right">
                    {f.symlink ? (
                      <span className="text-xs text-slate-400">Not a file</span>
                    ) : (
                      <button
                        onClick={() => handleDownload(f.path)}
                        className="text-xs text-indigo-300 hover:text-indigo-200"
                      >
                        Download
                      </button>
                    )}
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

// ── Checkpoints Tab ──────────────────────────────────────────────────────────
function CheckpointsTab({
  sessionId,
  checkpoints,
  onReload,
}: {
  sessionId: string;
  checkpoints: Checkpoint[];
  onReload: () => void;
}) {
  const router = useRouter();
  const toast = useToast();
  const [selectedCheckpoint, setSelectedCheckpoint] = useState<Checkpoint | null>(null);
  const [restoring, setRestoring] = useState(false);
  const [forking, setForking] = useState(false);
  const [forkName, setForkName] = useState("");
  const [confirmRestore, setConfirmRestore] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  const handleRestore = async (checkpointId: string) => {
    setRestoring(true);
    try {
      await api.checkpoints.restore(sessionId, checkpointId, false);
      toast.success("Checkpoint restored");
      onReload();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Restore failed");
    } finally {
      setRestoring(false);
      setConfirmRestore(null);
    }
  };

  const handleFork = async (checkpointId: string) => {
    const name = forkName.trim() || `Fork from ${checkpointId.slice(0, 8)}`;
    setForking(true);
    try {
      const { session_id } = await api.checkpoints.fork(checkpointId, name);
      router.push(`/sessions/${session_id}`);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Fork failed");
    } finally {
      setForking(false);
      setForkName("");
    }
  };

  const handleDelete = async (checkpointId: string) => {
    setConfirmDeleteId(null);
    try {
      await api.checkpoints.delete(sessionId, checkpointId);
      onReload();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Delete failed");
    }
  };

  return (
    <div className="space-y-4">
      <ConfirmDialog
        open={confirmRestore !== null}
        title="Restore this checkpoint?"
        message="This overwrites the current workspace with the state of the checkpoint."
        confirmLabel="Restore"
        busyLabel="Restoring…"
        busy={restoring}
        onConfirm={() => confirmRestore && handleRestore(confirmRestore)}
        onCancel={() => !restoring && setConfirmRestore(null)}
      />
      <ConfirmDialog
        open={confirmDeleteId !== null}
        title="Delete this checkpoint?"
        message="The checkpoint is removed and cannot be restored afterwards."
        onConfirm={() => confirmDeleteId && handleDelete(confirmDeleteId)}
        onCancel={() => setConfirmDeleteId(null)}
      />
      {checkpoints.length === 0 && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-8 text-center">
          <History className="w-12 h-12 mx-auto mb-3 text-slate-300" />
          <p className="text-slate-300 text-sm mb-2">No checkpoints yet</p>
          <p className="text-slate-200 text-xs">
            Checkpoints are automatically created after each command execution when replay is enabled.
          </p>
        </div>
      )}

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {checkpoints.length > 0 && (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">#</th>
                <th className="px-4 py-3 text-left">Description</th>
                <th className="px-4 py-3 text-left">Command</th>
                <th className="px-4 py-3 text-left">Size</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {checkpoints.map((cp) => (
                <tr
                  key={cp.id}
                  onClick={() => setSelectedCheckpoint(selectedCheckpoint?.id === cp.id ? null : cp)}
                  className={`cursor-pointer hover:bg-slate-800/20 transition-colors ${
                    selectedCheckpoint?.id === cp.id ? "bg-slate-800/30" : ""
                  }`}
                >
                  <td className="px-4 py-2.5 font-mono text-xs text-indigo-300">
                    #{cp.checkpoint_number}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200 max-w-xs truncate">
                    {cp.name || cp.description}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-slate-200 max-w-xs truncate">
                    {cp.command || "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200">
                    {formatBytes(cp.size_bytes)}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200">
                    {formatDate(cp.created_at)}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    <div className="flex items-center justify-end gap-2">
                      <button
                        onClick={(e) => { e.stopPropagation(); setConfirmRestore(cp.id); }}
                        disabled={restoring}
                        className="flex items-center gap-1 px-2 py-1 text-xs text-emerald-300 border border-emerald-900/50 rounded hover:bg-emerald-900/20 disabled:opacity-60"
                        title="Restore to this checkpoint"
                      >
                        <RotateCcw className="w-3 h-3" /> Restore
                      </button>
                      <button
                        onClick={(e) => { e.stopPropagation(); handleFork(cp.id); }}
                        disabled={forking}
                        className="flex items-center gap-1 px-2 py-1 text-xs text-indigo-300 border border-indigo-900/50 rounded hover:bg-indigo-900/20 disabled:opacity-60"
                        title="Fork new session from this checkpoint"
                      >
                        <GitFork className="w-3 h-3" /> Fork
                      </button>
                      <button
                        onClick={(e) => { e.stopPropagation(); setConfirmDeleteId(cp.id); }}
                        className="flex items-center gap-1 px-2 py-1 text-xs text-red-300 border border-red-900/50 rounded hover:bg-red-900/20"
                        title="Delete checkpoint"
                        aria-label="Delete checkpoint"
                      >
                        <Trash2 className="w-3 h-3" aria-hidden="true" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {selectedCheckpoint && (
        <div className="bg-[#060609] border border-slate-800 rounded-lg p-4 space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-xs text-slate-200 font-mono">
              Checkpoint #{selectedCheckpoint.checkpoint_number}
            </span>
            <span className="text-xs text-slate-200">{selectedCheckpoint.id.slice(0, 16)}</span>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <div className="text-xs text-slate-200 mb-1">Exit Code</div>
              <div className="text-sm text-slate-300 font-mono">
                {selectedCheckpoint.exit_code ?? "—"}
              </div>
            </div>
            <div>
              <div className="text-xs text-slate-200 mb-1">Duration</div>
              <div className="text-sm text-slate-300 font-mono">
                {selectedCheckpoint.duration_ms ? formatDuration(selectedCheckpoint.duration_ms) : "—"}
              </div>
            </div>
          </div>

          {selectedCheckpoint.stdout_preview && (
            <div>
              <div className="text-xs text-green-400 mb-1 uppercase tracking-wide">stdout (preview)</div>
              <pre className="text-green-300 bg-black/30 rounded p-3 max-h-32 overflow-auto text-xs font-mono whitespace-pre-wrap">
                {selectedCheckpoint.stdout_preview}
              </pre>
            </div>
          )}

          {selectedCheckpoint.stderr_preview && (
            <div>
              <div className="text-xs text-red-300 mb-1 uppercase tracking-wide">stderr (preview)</div>
              <pre className="text-red-300 bg-black/30 rounded p-3 max-h-32 overflow-auto text-xs font-mono whitespace-pre-wrap">
                {selectedCheckpoint.stderr_preview}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ── Audit Tab ─────────────────────────────────────────────────────────────────
function AuditTab({ sessionId }: { sessionId: string }) {
  const { isMaster } = useApiKey();
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    api.audit
      .list({ sessionId, limit: 200 })
      .then((r) => {
        if (cancelled) return;
        setLogs(r.logs);
        setTotal(r.total);
      })
      .catch((e: unknown) => !cancelled && setError(e instanceof Error ? e.message : "Failed to load audit log"))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  if (loading) return <div className="p-6 text-center text-slate-300 text-sm" role="status">Loading audit log…</div>;
  if (error) return <Notice tone="error" title="Could not load the audit log">{error}</Notice>;

  return (
    <div className="space-y-2">
      {isMaster === false && (
        <p className="text-xs text-slate-300">Entries recorded under other keys are not listed here.</p>
      )}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {logs.length === 0 ? (
          <div className="p-6 text-center text-slate-300 text-sm">No audit events for this session.</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
                <th scope="col" className="px-4 py-3 text-left font-medium">#</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Time</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Actor</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Action</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Details</th>
                <th scope="col" className="px-4 py-3 text-left font-medium">Signature</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {logs.map((l) => (
                <tr key={l.id} className="align-top hover:bg-slate-800/20">
                  <td className="px-4 py-2.5 text-xs text-slate-200 font-mono whitespace-nowrap">
                    {l.seq ?? <span className="text-slate-300" title="Written before chaining began">—</span>}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-slate-200 font-mono whitespace-nowrap">{formatDate(l.timestamp)}</td>
                  <td className="px-4 py-2.5 text-xs text-slate-200 font-mono" title={l.actor_name ? `${l.actor_name} (${l.actor})` : l.actor}>
                    {actorLabel(l)}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-indigo-200">{l.action}</td>
                  <td className="px-4 py-2.5 text-xs">
                    <MetadataInline meta={l.metadata} />
                  </td>
                  <td className="px-4 py-2.5">
                    <SigCell log={l} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {total > logs.length && (
        <p className="text-xs text-slate-200">
          Showing the latest {logs.length} of {total} events. <Link href="/audit" className="text-indigo-300 underline">Open the audit log</Link> for the full history.
        </p>
      )}
    </div>
  );
}
