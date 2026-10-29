"use client";

import { useEffect, useState } from "react";
import { Plus, Trash2, Ban, RefreshCw, Copy, Check, AlertCircle, Clock } from "lucide-react";
import { api } from "@/lib/api";
import { useApiKey } from "@/components/ApiKeyGate";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { MasterKeyNotice } from "@/components/ui/Notice";
import { PersonalKeyCard } from "@/components/PersonalKeyCard";
import { useToast } from "@/components/ui/Toast";
import { Time } from "@/components/ui/Time";
import { formatDateWithZone, toDateTimeLocalValue } from "@/lib/utils";
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

const DAY_MS = 24 * 60 * 60 * 1000;

/** An exact instant N days from now, as RFC 3339 (UTC on the wire). */
function inDays(days: number): string {
  return new Date(Date.now() + days * DAY_MS).toISOString();
}

/**
 * The datetime-local input holds local wall-clock time without a zone;
 * new Date() reads it as local, so toISOString() is the right instant.
 */
function localInputToRFC3339(datetimeLocal: string): string {
  return new Date(datetimeLocal).toISOString();
}

function isExpired(expiresAt: string): boolean {
  return new Date(expiresAt).getTime() < Date.now();
}

export default function KeysPage() {
  const { isMaster, roleReady, isSSO } = useApiKey();
  const toast = useToast();
  const [secretsInput, setSecretsInput] = useState("");
  const [revoking, setRevoking] = useState<APIKey | null>(null);
  const [revokeBusy, setRevokeBusy] = useState(false);
  const [deleting, setDeleting] = useState<APIKey | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
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
    if (!roleReady) return;
    if (isMaster === false) {
      setLoading(false);
      return;
    }
    setLoading(true);
    api.keys
      .list()
      .then(setKeys)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(load, [isMaster, roleReady]);

  const computeExpiresAt = (): string | undefined => {
    switch (preset) {
      case "never": return undefined;
      case "7d":    return inDays(7);
      case "30d":   return inDays(30);
      case "90d":   return inDays(90);
      case "1y":    return inDays(365);
      case "custom": return customDate ? localInputToRFC3339(customDate) : undefined;
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
      const secrets = secretsInput
        .split(/[\s,]+/)
        .map((x) => x.trim())
        .filter(Boolean);
      const created = await api.keys.create(name.trim(), computeExpiresAt(), secrets.length > 0 ? secrets : undefined);
      setNewKey(created);
      setName("");
      setPreset("never");
      setCustomDate("");
      setSecretsInput("");
      setShowForm(false);
      load();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create key");
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async () => {
    if (!revoking) return;
    setRevokeBusy(true);
    try {
      await api.keys.revoke(revoking.id);
      toast.success(`Key ${revoking.name} revoked`);
      setRevoking(null);
      load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to revoke key");
    } finally {
      setRevokeBusy(false);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    setDeleteBusy(true);
    try {
      await api.keys.remove(deleting.id);
      toast.success(`Key ${deleting.name} deleted`);
      setDeleting(null);
      load();
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to delete key");
    } finally {
      setDeleteBusy(false);
    }
  };

  const copyKey = async () => {
    if (!newKey) return;
    await navigator.clipboard.writeText(newKey.key);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="space-y-6">
      <ConfirmDialog
        open={revoking !== null}
        title="Revoke API key?"
        message={
          <>
            Key <span className="font-mono">{revoking?.name}</span> stops working immediately. This cannot be undone.
          </>
        }
        confirmLabel="Revoke key"
        busyLabel="Revoking…"
        busy={revokeBusy}
        onConfirm={handleRevoke}
        onCancel={() => !revokeBusy && setRevoking(null)}
      />
      <ConfirmDialog
        open={deleting !== null}
        title="Delete revoked key?"
        message={
          <>
            Removes <span className="font-mono">{deleting?.name}</span> from the list and frees its name. The key already
            stopped working when it was revoked.
          </>
        }
        confirmLabel="Delete key"
        busyLabel="Deleting…"
        busy={deleteBusy}
        onConfirm={handleDelete}
        onCancel={() => !deleteBusy && setDeleting(null)}
      />
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">API Keys</h1>
          <p className="text-slate-300 text-sm mt-1">
            Manage keys used to authenticate with the VaultRun API
          </p>
        </div>
        {isMaster !== false && (
          <div className="flex gap-2">
            <button
              onClick={load}
              className="flex items-center gap-1.5 px-3 py-2 text-sm text-slate-300 border border-slate-700 rounded-md hover:bg-slate-800"
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
        )}
      </div>

      {isSSO && <PersonalKeyCard />}

      {isMaster === false && <MasterKeyNotice what="Creating, listing and revoking API keys" />}

      {error && isMaster !== false && (
        <div role="alert" className="flex items-center gap-2 text-red-400 bg-red-950/30 border border-red-900/50 rounded-lg px-4 py-3 text-sm">
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
            <p className="text-xs text-emerald-300 flex items-center gap-1">
              <Clock className="w-3 h-3" />
              Expires {formatDateWithZone(newKey.expires_at)}
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
            className="text-xs text-slate-200 hover:text-slate-200"
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
            <label htmlFor="key-name" className="block text-xs text-slate-200 mb-1">Key name</label>
            <input
              id="key-name"
              className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && handleCreate()}
              placeholder="my-agent-key"
              autoFocus
            />
          </div>

          <div>
            <label htmlFor="key-secrets" className="block text-xs text-slate-200 mb-1">Allowed secrets (optional)</label>
            <input
              id="key-secrets"
              className="w-full bg-slate-900 border border-slate-700 rounded px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-400"
              value={secretsInput}
              onChange={(e) => setSecretsInput(e.target.value)}
              placeholder="DB_PASSWORD, API_TOKEN"
            />
            <p className="mt-1 text-xs text-slate-200">Comma-separated secret names this key may inject into runs. Leave empty to allow any secret.</p>
          </div>

          <div>
            <span className="block text-xs text-slate-200 mb-2">Expiry</span>
            <div className="flex flex-wrap gap-2">
              {PRESETS.map((p) => (
                <button
                  key={p.value}
                  type="button"
                  onClick={() => setPreset(p.value)}
                  className={`px-3 py-1.5 rounded text-xs font-medium transition-colors ${
                    preset === p.value
                      ? "bg-indigo-600 text-white"
                      : "bg-slate-800 text-slate-300 hover:bg-slate-700 hover:text-slate-200"
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
                min={toDateTimeLocalValue(new Date())}
                onChange={(e) => setCustomDate(e.target.value)}
              />
            )}
          </div>

          <div className="flex gap-2">
            <button
              onClick={handleCreate}
              disabled={creating || !name.trim()}
              className="px-4 py-2 text-sm text-white bg-indigo-600 rounded hover:bg-indigo-500 disabled:opacity-60"
            >
              {creating ? "Creating…" : "Create Key"}
            </button>
            <button
              onClick={() => { setShowForm(false); setName(""); setPreset("never"); setCustomDate(""); }}
              className="px-4 py-2 text-sm text-slate-300 border border-slate-700 rounded hover:bg-slate-800"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Keys table */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {isMaster === false ? (
          <div className="p-8 text-center text-slate-300 text-sm">The key list is only visible to the master key.</div>
        ) : loading ? (
          <div className="p-8 text-center text-slate-300 text-sm" role="status">Loading…</div>
        ) : keys.length === 0 ? (
          <div className="p-8 text-center text-slate-300 text-sm">
            No API keys yet. Click &ldquo;New Key&rdquo; to create one.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Prefix</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Secrets</th>
                <th className="px-4 py-3 text-left">Expires</th>
                <th className="px-4 py-3 text-left">Created</th>
                <th className="px-4 py-3 text-left">Last used</th>
                <th className="px-4 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {keys.map((k) => {
                const expired = k.expires_at ? isExpired(k.expires_at) : false;
                const showExpired = k.active && expired;
                return (
                  <tr key={k.id} className="hover:bg-slate-800/20 transition-colors">
                    <td className="px-4 py-3 text-slate-200 font-medium">{k.name}</td>
                    <td className="px-4 py-3 font-mono text-xs text-slate-200">{k.prefix}…</td>
                    <td className="px-4 py-3">
                      {showExpired ? (
                        <span className="inline-flex items-center gap-1 text-xs text-amber-300">
                          <span className="w-1.5 h-1.5 rounded-full bg-amber-400" />
                          Expired
                        </span>
                      ) : k.active ? (
                        <span className="inline-flex items-center gap-1 text-xs text-emerald-300">
                          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                          Active
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-xs text-slate-200">
                          <span className="w-1.5 h-1.5 rounded-full bg-slate-600" />
                          Revoked
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-slate-200">
                      {k.allowed_secrets == null ? (
                        <span className="text-slate-300">Any</span>
                      ) : k.allowed_secrets.length === 0 ? (
                        <span className="text-amber-300">None</span>
                      ) : (
                        <span className="font-mono" title={k.allowed_secrets.join(", ")}>{k.allowed_secrets.length} allowed</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs">
                      {k.expires_at ? (
                        <span className={expired ? "text-amber-400" : "text-slate-300"} title={new Date(k.expires_at).toISOString()}>
                          <Clock className="w-3 h-3 inline mr-1 opacity-60" aria-hidden="true" />
                          {formatDateWithZone(k.expires_at)}
                        </span>
                      ) : (
                        <span className="text-slate-300">Never</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-slate-200">
                      <Time value={k.created_at} />
                    </td>
                    <td className="px-4 py-3 text-xs text-slate-200">
                      {k.last_used_at ? <Time value={k.last_used_at} /> : <span className="text-slate-300">Never</span>}
                    </td>
                    <td className="px-4 py-3 text-right">
                      {k.active ? (
                        <button
                          onClick={() => setRevoking(k)}
                          className="text-slate-300 hover:text-red-400 transition-colors"
                          title="Revoke key"
                          aria-label={`Revoke key ${k.name}`}
                        >
                          <Ban className="w-4 h-4" aria-hidden="true" />
                        </button>
                      ) : (
                        <button
                          onClick={() => setDeleting(k)}
                          className="text-slate-300 hover:text-red-400 transition-colors"
                          title="Delete revoked key"
                          aria-label={`Delete revoked key ${k.name}`}
                        >
                          <Trash2 className="w-4 h-4" aria-hidden="true" />
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
