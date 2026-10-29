"use client";

import { useState } from "react";
import { Copy, Check, KeyRound } from "lucide-react";
import { sso } from "@/lib/api";
import { useToast } from "@/components/ui/Toast";

/**
 * For users signed in with single sign-on: issue a personal API key for the
 * CLI, SDKs or MCP. The key acts as the user (same access as the dashboard
 * session) and is shown once; creating a new one retires the previous one.
 */
export function PersonalKeyCard() {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [key, setKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const create = async () => {
    setBusy(true);
    try {
      const res = await sso.createApiKey();
      setKey(res.api_key);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not create an API key");
    } finally {
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!key) return;
    await navigator.clipboard.writeText(key);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-5 space-y-3">
      <h2 className="text-sm font-medium text-slate-100 flex items-center gap-2">
        <KeyRound className="w-4 h-4" aria-hidden="true" /> Your personal API key
      </h2>
      <p className="text-sm text-slate-300">
        You signed in with single sign-on. For the CLI, the SDKs or MCP, create a personal API key: it has the same
        access as your dashboard session. It is shown once; creating a new one stops the previous one from working.
      </p>
      {key ? (
        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <code className="flex-1 min-w-0 break-all bg-slate-900 border border-slate-700 rounded px-3 py-2 text-xs font-mono text-slate-100">
              {key}
            </code>
            <button
              onClick={copy}
              aria-label="Copy API key"
              className="p-2 rounded border border-slate-700 text-slate-200 hover:bg-slate-800"
            >
              {copied ? <Check className="w-4 h-4" aria-hidden="true" /> : <Copy className="w-4 h-4" aria-hidden="true" />}
            </button>
          </div>
          <p className="text-xs text-amber-300">Copy it now: it will not be shown again.</p>
        </div>
      ) : (
        <button
          onClick={create}
          disabled={busy}
          className="px-3 py-2 text-sm text-white bg-indigo-600 rounded-md hover:bg-indigo-500 disabled:opacity-60"
        >
          {busy ? "Creating…" : "Create API key"}
        </button>
      )}
    </div>
  );
}
