"use client";

import { useEffect, useState } from "react";
import { Plus, Trash2, RefreshCw, Copy, Check, AlertCircle, Clock } from "lucide-react";
import { api } from "@/lib/api";
import type { APIKey, CreatedKey } from "@/types";

type ExpiryPreset = "never" | "7d" | "30d" | "90d" | "1y" | "custom";

const PRESETS: { value: ExpiryPreset; label: string }[] = [
  { value: "never", label: "Never" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
  { value: "90d", label: "90 days" },
  { value: "1y", label: "1 year" },
  { value: "custom", label: "Custom…" },
];

function addDays(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  return d.toISOString().slice(0, 16); // "YYYY-MM-DDTHH:MM" for datetime-local
}

function toRFC3339(datetimeLocal: string): string {
  return new Date(datetimeLocal).toISOString();
}

function formatExpiry(expiresAt: string): { label: string; expired: boolean } {
  const d = new Date(expiresAt);
  const now = new Date();
  const expired = d < now;
  return {
    label: d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }),
    expired,
  };
}

export default function KeysPage() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [preset, setPreset] = useState<ExpiryPreset>("never");
  const [customDate, setCustomDate] = useState("");
  const [creating, setCreating] = useState(false);
  const [newKey, setNewKey] = useState<CreatedKey | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = () => {
    setLoading(true);
    api.keys
      .list()
      .then(setKeys)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const computeExpiresAt = (): string | undefined => {
    switch (preset) {
      case "never": return undefined;
      case "7d":    return toRFC3339(addDays(7));
      case "30d":   return toRFC3339(addDays(30));
      case "90d":   return toRFC3339(addDays(90));
      case "1y":    return toRFC3339(addDays(365));
      case "custom": return customDate ? toRFC3339(customDate) : undefined;
    }
  };

  const handleCreate = async () => {
    if (!name.trim()) return;
    if (preset === "custom" && !customDate) {
      setError("Pick a custom expiry date or choose a preset");
      return;
    }
    setCreating(true);
    setError(null);
    try {
      const created = await api.keys.create(name.trim(), computeExpiresAt());
      setNewKey(created);
      setName("");
      setPreset("never");
      setCustomDate("");
      setShowForm(false);
      load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create key");
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async (id: string, keyName: string) => {
    if (!confirm(`Revoke key "${keyName}"? This cannot be undone.`)) return;
    try {
      await api.keys.revoke(id);
      load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to revoke key");
    }
  };

  const copyKey = async () => {
    if (!newKey) return;
    await navigator.clipboard.writeText(newKey.key);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="max-w-5xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">API Keys</h1>
          <p className="text-slate-500 text-sm mt-1">
            Manage keys used to authenticate with the VaultRun API
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={load}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-400 border border-slate-700 rounded-md hover:bg-slate-800"
          >
            <RefreshCw className="w-3.5 h-3.5" /> Refresh
          </button>
          <button
            onClick={() => { setShowForm(!showForm); setError(null); }}
            className="flex items-center gap-1.5 px-3 py-2 text-sm text-white bg-indigo-600 rounded-md hover:bg-indigo-500"
          >
            <Plus className="w-3.5 h-3.5" /> New Key
          </button>
        </div>
      </div>

      {error && (
        <div className="flex items-center gap-2 text-red-400 bg-red-950/30 border border-red-900/50 rounded-lg px-4 py-3 text-sm">
          <AlertCircle className="w-4 h-4 flex-shrink-0" />
          {error}
        </div>
      )}

      {/* One-time key reveal */}
      {newKey && (
        <div className="bg-emerald-950/30 border border-emerald-800/50 rounded-lg p-5 space-y-3">
          <div className="flex items-center gap-2">
            <span className="w-2 h-2 rounded-full bg-emerald-400" />
            <p className="text-sm font-medium text-emerald-300">
              Key &ldquo;{newKey.name}&rdquo; created — copy it now, it won&apos;t be shown again.
            </p>
          </div>
          {newKey.expires_at && (
            <p className="text-xs text-emerald-600 flex items-center gap-1">
              <Clock className="w-3 h-3" />
              Expires {new Date(newKey.expires_at).toLocaleString()}
            </p>
          )}
          <div className="flex items-center gap-2">
            <code className="flex-1 bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-emerald-300 break-all">
              {newKey.key}
            </code>
            <button
              onClick={copyKey}
              className="flex items-center gap-1.5 px-3 py-2 text-sm border border-slate-700 rounded hover:bg-slate-800 text-slate-300 whitespace-nowrap"
            >
              {copied ? (
                <><Check className="w-4 h-4 text-emerald-400" /> Copied</>
              ) : (
                <><Copy className="w-4 h-4" /> Copy</>
              )}
            </button>
          </div>
          <button
            onClick={() => setNewKey(null)}
            className="text-xs text-slate-500 hover:text-slate-400"
          >
            I&apos;ve saved it — dismiss
          </button>
        </div>
      )}

      {/* Create form */}
      {showForm && (
        <div className="bg-[#0f0f1a] border border-slate-700 rounded-lg p-5 space-y-4">
          <h2 className="text-sm font-medium text-slate-300">Create New API Key</h2>

          <div>
            <label className="block text-xs text-slate-500 mb-1">Key name</label>
            <input
              className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleCreate()}
              placeholder="my-agent-key"
              autoFocus
            />
          </div>

          <div>
            <label className="block text-xs text-slate-500 mb-2">Expiry</label>
            <div className="flex flex-wrap gap-2">
              {PRESETS.map((p) => (
                <button
                  key={p.value}
                  type="button"
                  onClick={() => setPreset(p.value)}
                  className={`px-3 py-1.5 rounded text-xs font-medium transition-colors ${
                    preset === p.value
                      ? "bg-indigo-600 text-white"
                      : "bg-slate-800 text-slate-400 hover:bg-slate-700 hover:text-slate-200"
                  }`}
                >
                  {p.label}
                </button>
              ))}
            </div>
            {preset === "custom" && (
              <input
                type="datetime-local"
                className="mt-2 bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
                value={customDate}
                min={new Date().toISOString().slice(0, 16)}
                onChange={(e) => setCustomDate(e.target.value)}
              />
            )}
          </div>

          <div className="flex gap-2">
            <button
              onClick={handleCreate}
              disabled={creating || !name.trim()}
              className="px-4 py-2 text-sm text-white bg-indigo-600 rounded hover:bg-indigo-500 disabled:opacity-50"
            >
              {creating ? "Creating…" : "Create Key"}
            </button>
            <button
              onClick={() => { setShowForm(false); setName(""); setPreset("never"); setCustomDate(""); }}
              className="px-4 py-2 text-sm text-slate-400 border border-slate-700 rounded hover:bg-slate-800"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Keys table */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-600 text-sm">Loading…</div>
        ) : keys.length === 0 ? (
          <div className="p-8 text-center text-slate-600 text-sm">
            No API keys yet. Click &ldquo;New Key&rdquo; to create one.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Prefix</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Expires</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th className="px-4 py-3 text-left">Last used</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {keys.map((k) => {
                const expiry = k.expires_at ? formatExpiry(k.expires_at) : null;
                const isExpired = k.active && expiry?.expired;
                return (
                  <tr key={k.id} className="hover:bg-slate-800/20 transition-colors">
                    <td className="px-4 py-3 text-slate-200 font-medium">{k.name}</td>
                    <td className="px-4 py-3 font-mono text-xs text-slate-400">{k.prefix}…</td>
                    <td className="px-4 py-3">
                      {isExpired ? (
                        <span className="inline-flex items-center gap-1 text-xs text-amber-400">
                          <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
                          Expired
                        </span>
                      ) : k.active ? (
                        <span className="inline-flex items-center gap-1 text-xs text-emerald-400">
                          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                          Active
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-xs text-slate-500">
                          <span className="w-1.5 h-1.5 rounded-full bg-slate-600" />
                          Revoked
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs">
                      {expiry ? (
                        <span className={expiry.expired ? "text-amber-500" : "text-slate-400"}>
                          <Clock className="w-3 h-3 inline mr-1 opacity-60" />
                          {expiry.label}
                        </span>
                      ) : (
                        <span className="text-slate-600">Never</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-slate-500">
                      {new Date(k.created_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-3 text-xs text-slate-500">
                      {k.last_used_at
                        ? new Date(k.last_used_at).toLocaleString()
                        : <span className="text-slate-700">Never</span>}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {k.active && (
                        <button
                          onClick={() => handleRevoke(k.id, k.name)}
                          className="text-slate-600 hover:text-red-400 transition-colors"
                          title="Revoke key"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
