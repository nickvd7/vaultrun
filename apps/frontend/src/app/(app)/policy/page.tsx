"use client";

import { useEffect, useState } from "react";
import { RefreshCw, Play, CheckCircle, XCircle, ShieldCheck, ShieldOff } from "lucide-react";
import { api } from "@/lib/api";
import type { PolicyStatus, PolicyEvalResult } from "@/types";

type EvalTab = "command" | "file";

export default function PolicyPage() {
  const [status, setStatus] = useState<PolicyStatus | null>(null);
  const [loading, setLoading] = useState(true);

  // eval form
  const [tab, setTab] = useState<EvalTab>("command");
  const [command, setCommand] = useState("python");
  const [args, setArgs] = useState("script.py");
  const [filePath, setFilePath] = useState("/workspace/output.txt");
  const [fileWrite, setFileWrite] = useState(false);
  const [evaluating, setEvaluating] = useState(false);
  const [result, setResult] = useState<PolicyEvalResult | null>(null);
  const [evalError, setEvalError] = useState("");

  const load = () => {
    setLoading(true);
    api.policy
      .get()
      .then(setStatus)
      .catch(console.error)
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const handleEval = async () => {
    setEvaluating(true);
    setResult(null);
    setEvalError("");
    try {
      const req =
        tab === "command"
          ? { type: "command" as const, command, args: args.split(/\s+/).filter(Boolean) }
          : { type: "file" as const, path: filePath, write: fileWrite };
      const res = await api.policy.eval(req);
      setResult(res);
    } catch (e: unknown) {
      setEvalError(e instanceof Error ? e.message : "Evaluation failed");
    } finally {
      setEvaluating(false);
    }
  };

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Policy</h1>
          <p className="text-slate-500 text-sm mt-1">
            OPA Rego policy controlling command and file access
          </p>
        </div>
        <button
          onClick={load}
          disabled={loading}
          className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-400 border border-slate-700 rounded-md hover:bg-slate-800 disabled:opacity-50"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
      </div>

      {/* Status card */}
      {!loading && status && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-5 space-y-3">
          <div className="flex items-center gap-3">
            {status.enabled ? (
              <>
                <ShieldCheck className="w-5 h-5 text-emerald-400 shrink-0" />
                <div>
                  <p className="text-sm font-medium text-emerald-300">OPA policy active</p>
                  {status.file_path && (
                    <p className="text-xs text-slate-500 font-mono mt-0.5">{status.file_path}</p>
                  )}
                </div>
                <span className="ml-auto inline-flex items-center gap-1 text-xs text-emerald-400 bg-emerald-900/30 border border-emerald-800/50 px-2 py-0.5 rounded">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                  Enforcing
                </span>
              </>
            ) : (
              <>
                <ShieldOff className="w-5 h-5 text-slate-500 shrink-0" />
                <div>
                  <p className="text-sm font-medium text-slate-400">AllowAll (no policy file)</p>
                  <p className="text-xs text-slate-600 mt-0.5">
                    Set <code className="font-mono text-slate-500">OPA_POLICY_FILE</code> to a{" "}
                    <code className="font-mono text-slate-500">.rego</code> file to enforce rules
                  </p>
                </div>
                <span className="ml-auto inline-flex items-center gap-1 text-xs text-slate-500 bg-slate-800 border border-slate-700 px-2 py-0.5 rounded">
                  <span className="w-1.5 h-1.5 rounded-full bg-slate-600" />
                  Passthrough
                </span>
              </>
            )}
          </div>

          {status.error && (
            <p className="text-xs text-red-400 bg-red-950/30 border border-red-900/40 rounded px-3 py-2">
              {status.error}
            </p>
          )}

          {/* Policy content */}
          {status.enabled && status.content && (
            <div className="mt-2">
              <p className="text-xs text-slate-600 uppercase tracking-wide mb-2">Policy source</p>
              <pre className="bg-slate-950 border border-slate-800 rounded-lg px-4 py-3 text-xs font-mono text-slate-300 overflow-x-auto leading-relaxed whitespace-pre">
                {status.content}
              </pre>
            </div>
          )}
        </div>
      )}

      {loading && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-8 text-center text-slate-600 text-sm">
          Loading…
        </div>
      )}

      {/* Test panel */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-5 space-y-4">
        <h2 className="text-sm font-medium text-slate-300">Policy Test</h2>
        <p className="text-xs text-slate-500">
          Dry-run an input against the currently loaded policy without executing anything.
        </p>

        {/* Tabs */}
        <div className="flex gap-1">
          {(["command", "file"] as EvalTab[]).map((t) => (
            <button
              key={t}
              onClick={() => { setTab(t); setResult(null); setEvalError(""); }}
              className={`px-3 py-1.5 rounded text-xs font-medium transition-colors capitalize ${
                tab === t
                  ? "bg-indigo-600 text-white"
                  : "bg-slate-800 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
              }`}
            >
              {t === "command" ? "Command" : "File access"}
            </button>
          ))}
        </div>

        {tab === "command" ? (
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs text-slate-500 mb-1">Command</label>
              <input
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
                value={command}
                onChange={(e) => setCommand(e.target.value)}
                placeholder="python"
              />
            </div>
            <div>
              <label className="block text-xs text-slate-500 mb-1">Args (space-separated)</label>
              <input
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
                value={args}
                onChange={(e) => setArgs(e.target.value)}
                placeholder="script.py --verbose"
              />
            </div>
          </div>
        ) : (
          <div className="flex items-end gap-4">
            <div className="flex-1">
              <label className="block text-xs text-slate-500 mb-1">File path</label>
              <input
                className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500"
                value={filePath}
                onChange={(e) => setFilePath(e.target.value)}
                placeholder="/workspace/output.txt"
              />
            </div>
            <label className="flex items-center gap-2 text-sm text-slate-400 pb-2 cursor-pointer">
              <input
                type="checkbox"
                checked={fileWrite}
                onChange={(e) => setFileWrite(e.target.checked)}
                className="rounded"
              />
              Write
            </label>
          </div>
        )}

        <div className="flex items-center gap-3">
          <button
            onClick={handleEval}
            disabled={evaluating}
            className="flex items-center gap-1.5 px-4 py-2 text-sm text-white bg-indigo-600 rounded hover:bg-indigo-500 disabled:opacity-50"
          >
            <Play className="w-3.5 h-3.5" />
            {evaluating ? "Evaluating…" : "Evaluate"}
          </button>

          {/* Inline result */}
          {result && (
            <div className={`flex items-center gap-2 text-sm font-medium ${result.allowed ? "text-emerald-400" : "text-red-400"}`}>
              {result.allowed ? (
                <CheckCircle className="w-4 h-4" />
              ) : (
                <XCircle className="w-4 h-4" />
              )}
              {result.allowed ? "Allowed" : `Denied${result.reason ? `: ${result.reason}` : ""}`}
            </div>
          )}

          {evalError && (
            <p className="text-sm text-red-400">{evalError}</p>
          )}
        </div>
      </div>
    </div>
  );
}
