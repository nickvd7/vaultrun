"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useId, useMemo, useState } from "react";
import { Plus, Trash2, RefreshCw, Terminal, Search, ArrowUp, ArrowDown, ChevronLeft, ChevronRight } from "lucide-react";
import { api } from "@/lib/api";
import { StatusBadge } from "@/components/StatusBadge";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { Notice } from "@/components/ui/Notice";
import { useToast } from "@/components/ui/Toast";
import { Time } from "@/components/ui/Time";
import { useOrg } from "@/components/OrgContext";
import type { Session } from "@/types";

const DEFAULT_IMAGE = "python:3.12-slim";
const FETCH_LIMIT = 200;
const PAGE_SIZE = 20;

type SortKey = "created_at" | "name" | "status";
type SortDir = "asc" | "desc";

interface FormState {
  name: string;
  image: string;
  network_enabled: boolean;
  cpu_limit: string;
  memory_limit_mb: string;
  timeout_seconds: string;
  org_id: string;
  replay_enabled: boolean;
}

const emptyForm = (orgId: string): FormState => ({
  name: "",
  image: DEFAULT_IMAGE,
  network_enabled: false,
  cpu_limit: "1",
  memory_limit_mb: "512",
  timeout_seconds: "300",
  org_id: orgId,
  replay_enabled: false,
});

type FormErrors = Partial<Record<"cpu_limit" | "memory_limit_mb" | "timeout_seconds", string>>;

function validate(form: FormState): FormErrors {
  const errors: FormErrors = {};
  const cpu = Number(form.cpu_limit);
  if (form.cpu_limit.trim() === "" || !Number.isFinite(cpu) || cpu <= 0) {
    errors.cpu_limit = "Enter a CPU limit greater than 0 (for example 0.5 or 2).";
  }
  const mem = Number(form.memory_limit_mb);
  if (form.memory_limit_mb.trim() === "" || !Number.isInteger(mem) || mem <= 0) {
    errors.memory_limit_mb = "Enter a whole number of megabytes greater than 0.";
  }
  const timeout = Number(form.timeout_seconds);
  if (form.timeout_seconds.trim() === "" || !Number.isInteger(timeout) || timeout <= 0) {
    errors.timeout_seconds = "Enter a whole number of seconds greater than 0.";
  }
  return errors;
}

export default function SessionsPage() {
  const router = useRouter();
  const toast = useToast();
  const { orgs, orgId } = useOrg();
  const [sessions, setSessions] = useState<Session[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [creating, setCreating] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<FormState>(emptyForm(""));
  const [touched, setTouched] = useState(false);
  const [error, setError] = useState("");
  const [toDelete, setToDelete] = useState<Session | null>(null);
  const [deleting, setDeleting] = useState(false);

  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [sortKey, setSortKey] = useState<SortKey>("created_at");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [page, setPage] = useState(1);

  const load = () => {
    setLoading(true);
    setLoadError("");
    api.sessions
      .list(1, FETCH_LIMIT)
      .then(({ sessions: list, pagination }) => {
        setSessions(orgId ? list.filter((s) => s.org_id === orgId) : list);
        setTotal(pagination?.total ?? list.length);
      })
      .catch((e: unknown) => setLoadError(e instanceof Error ? e.message : "Failed to load sessions"))
      .finally(() => setLoading(false));
  };

  useEffect(load, [orgId]);

  useEffect(() => {
    if (orgId) setForm((f) => ({ ...f, org_id: orgId }));
  }, [orgId]);

  useEffect(() => setPage(1), [query, statusFilter, sortKey, sortDir]);

  const errors = validate(form);
  const hasErrors = Object.keys(errors).length > 0;

  const handleCreate = async () => {
    setTouched(true);
    if (hasErrors) return;
    setCreating(true);
    setError("");
    try {
      const created = await api.sessions.create({
        name: form.name.trim() || undefined,
        image: form.image.trim() || DEFAULT_IMAGE,
        network_enabled: form.network_enabled,
        cpu_limit: Number(form.cpu_limit),
        memory_limit_mb: Number(form.memory_limit_mb),
        timeout_seconds: Number(form.timeout_seconds),
        org_id: form.org_id || undefined,
        replay_enabled: form.replay_enabled,
      });
      toast.success(`Session ${created.name || created.id.slice(0, 8)} created`);
      router.push(`/sessions/${created.id}`);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create session");
      setCreating(false);
    }
  };

  const handleDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    try {
      await api.sessions.delete(toDelete.id);
      setSessions((prev) => prev.filter((s) => s.id !== toDelete.id));
      toast.success(`Session ${toDelete.name || toDelete.id.slice(0, 8)} deleted`);
      setToDelete(null);
    } catch (e: unknown) {
      toast.error(e instanceof Error ? e.message : "Failed to delete session");
    } finally {
      setDeleting(false);
    }
  };

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else {
      setSortKey(key);
      setSortDir(key === "created_at" ? "desc" : "asc");
    }
  };

  const statuses = useMemo(() => Array.from(new Set(sessions.map((s) => s.status))).sort(), [sessions]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    const filtered = sessions.filter((s) => {
      if (statusFilter !== "all" && s.status !== statusFilter) return false;
      if (!q) return true;
      const labels = Object.entries(s.labels ?? {}).map(([k, v]) => `${k}=${v}`);
      return [s.name ?? "", s.id, s.image, ...labels].some((f) => f.toLowerCase().includes(q));
    });
    const dir = sortDir === "asc" ? 1 : -1;
    return filtered.sort((a, b) => {
      if (sortKey === "created_at") return (new Date(a.created_at).getTime() - new Date(b.created_at).getTime()) * dir;
      const av = sortKey === "name" ? a.name || a.id : a.status;
      const bv = sortKey === "name" ? b.name || b.id : b.status;
      return av.localeCompare(bv) * dir;
    });
  }, [sessions, query, statusFilter, sortKey, sortDir]);

  const pageCount = Math.max(1, Math.ceil(visible.length / PAGE_SIZE));
  const currentPage = Math.min(page, pageCount);
  const pageItems = visible.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);
  const filtering = query.trim() !== "" || statusFilter !== "all";

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Sessions</h1>
          <p className="text-slate-300 text-sm mt-1">
            {filtering ? `${visible.length} of ${sessions.length}` : sessions.length} session
            {(filtering ? visible.length : sessions.length) !== 1 ? "s" : ""}
            {orgId ? " in selected org" : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={load}
            className="p-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-300 hover:text-slate-100 transition-colors"
            aria-label="Refresh sessions"
            title="Refresh"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? "animate-spin" : ""}`} aria-hidden="true" />
          </button>
          <button
            onClick={() => setShowForm(true)}
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
          >
            <Plus className="w-4 h-4" aria-hidden="true" /> New Session
          </button>
        </div>
      </div>

      {showForm && (
        <form
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            handleCreate();
          }}
          className="bg-[#0f0f1a] border border-slate-700 rounded-xl p-6 space-y-4"
        >
          <h2 className="text-sm font-medium text-slate-200">New Session</h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <Field label="Name (optional)">
              {(id) => (
                <input id={id} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="my-sandbox" className={input} />
              )}
            </Field>
            <Field label="Docker Image">
              {(id) => (
                <input id={id} value={form.image} onChange={(e) => setForm({ ...form, image: e.target.value })} placeholder={DEFAULT_IMAGE} className={input} />
              )}
            </Field>
            <Field label="CPU Limit" error={touched ? errors.cpu_limit : undefined}>
              {(id, describedBy) => (
                <input
                  id={id}
                  inputMode="decimal"
                  aria-invalid={touched && !!errors.cpu_limit}
                  aria-describedby={describedBy}
                  value={form.cpu_limit}
                  onChange={(e) => setForm({ ...form, cpu_limit: e.target.value })}
                  placeholder="1.0"
                  className={input}
                />
              )}
            </Field>
            <Field label="Memory (MB)" error={touched ? errors.memory_limit_mb : undefined}>
              {(id, describedBy) => (
                <input
                  id={id}
                  inputMode="numeric"
                  aria-invalid={touched && !!errors.memory_limit_mb}
                  aria-describedby={describedBy}
                  value={form.memory_limit_mb}
                  onChange={(e) => setForm({ ...form, memory_limit_mb: e.target.value })}
                  placeholder="512"
                  className={input}
                />
              )}
            </Field>
            <Field label="Timeout (seconds)" error={touched ? errors.timeout_seconds : undefined}>
              {(id, describedBy) => (
                <input
                  id={id}
                  inputMode="numeric"
                  aria-invalid={touched && !!errors.timeout_seconds}
                  aria-describedby={describedBy}
                  value={form.timeout_seconds}
                  onChange={(e) => setForm({ ...form, timeout_seconds: e.target.value })}
                  placeholder="300"
                  className={input}
                />
              )}
            </Field>
            {orgs.length > 0 && (
              <Field label="Organization">
                {(id) => (
                  <select id={id} value={form.org_id} onChange={(e) => setForm({ ...form, org_id: e.target.value })} className={input}>
                    <option value="">Personal (no org)</option>
                    {orgs.map((o) => (
                      <option key={o.id} value={o.id}>
                        {o.name}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
            )}
            <div>
              <span className="block text-xs text-slate-200 mb-1.5">Network</span>
              <label className="flex items-center gap-2 mt-2 cursor-pointer">
                <input type="checkbox" checked={form.network_enabled} onChange={(e) => setForm({ ...form, network_enabled: e.target.checked })} className="accent-indigo-500" />
                <span className="text-slate-300 text-sm">Enable network access</span>
              </label>
            </div>
            <div>
              <span className="block text-xs text-slate-200 mb-1.5">Replay</span>
              <label className="flex items-center gap-2 mt-2 cursor-pointer">
                <input type="checkbox" checked={form.replay_enabled} onChange={(e) => setForm({ ...form, replay_enabled: e.target.checked })} className="accent-indigo-500" />
                <span className="text-slate-300 text-sm">Enable session replay checkpoints</span>
              </label>
            </div>
          </div>
          {error && (
            <p role="alert" className="text-red-300 text-xs">
              {error}
            </p>
          )}
          <div className="flex items-center gap-3 pt-1">
            <button type="submit" disabled={creating} className="px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white text-sm font-medium transition-colors">
              {creating ? "Creating…" : "Create"}
            </button>
            <button
              type="button"
              onClick={() => {
                setShowForm(false);
                setError("");
                setTouched(false);
                setForm(emptyForm(orgId ?? ""));
              }}
              className="px-4 py-2 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-300 text-sm transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {loadError && <Notice tone="error" title="Could not load sessions">{loadError}</Notice>}

      <div className="flex flex-col sm:flex-row gap-3">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-300" aria-hidden="true" />
          <input
            type="search"
            aria-label="Search sessions by name, ID, image or label"
            placeholder="Search name, ID, image or label (demo=true)…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-full bg-[#0f0f1a] border border-slate-700 rounded-lg pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-400 focus:outline-none focus:border-indigo-500"
          />
        </div>
        <select
          aria-label="Filter by status"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          className="bg-[#0f0f1a] border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
        >
          <option value="all">All statuses</option>
          {statuses.map((s) => (
            <option key={s} value={s}>
              {s.charAt(0).toUpperCase() + s.slice(1)}
            </option>
          ))}
        </select>
      </div>

      {total > sessions.length && !orgId && (
        <p className="text-xs text-slate-200">Showing the {sessions.length} most recent of {total} sessions.</p>
      )}

      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        {loading ? (
          <div className="p-8 text-center text-slate-300 text-sm">Loading…</div>
        ) : sessions.length === 0 ? (
          <div className="p-10 text-center space-y-3">
            <Terminal className="w-8 h-8 text-slate-300 mx-auto" aria-hidden="true" />
            <p className="text-slate-300 text-sm">No sessions yet. Create one to get started.</p>
          </div>
        ) : visible.length === 0 ? (
          <div className="p-10 text-center text-slate-300 text-sm">No sessions match your search.</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-200 uppercase tracking-wide">
                <SortTh label="Session" active={sortKey === "name"} dir={sortDir} onClick={() => toggleSort("name")} />
                <th scope="col" className="px-4 py-3 text-left font-medium">Image</th>
                <SortTh label="Status" active={sortKey === "status"} dir={sortDir} onClick={() => toggleSort("status")} />
                <th scope="col" className="px-4 py-3 text-left font-medium">Resources</th>
                <SortTh label="Created" active={sortKey === "created_at"} dir={sortDir} onClick={() => toggleSort("created_at")} />
                <th scope="col" className="px-4 py-3">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {pageItems.map((s) => (
                <tr
                  key={s.id}
                  onClick={() => router.push(`/sessions/${s.id}`)}
                  className="hover:bg-slate-800/20 transition-colors cursor-pointer"
                >
                  <td className="px-4 py-3">
                    <Link
                      href={`/sessions/${s.id}`}
                      onClick={(e) => e.stopPropagation()}
                      className="font-mono text-indigo-200 text-xs hover:underline"
                    >
                      {s.name || `${s.id.slice(0, 12)}…`}
                    </Link>
                    {s.replay_enabled && <span className="ml-2 text-[10px] uppercase tracking-wide text-slate-300">replay</span>}
                    <LabelChips labels={s.labels} onPick={(kv) => setQuery(kv)} />
                  </td>
                  <td className="px-4 py-3 text-slate-200 font-mono text-xs">{s.image}</td>
                  <td className="px-4 py-3">
                    <StatusBadge status={s.status} />
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-200">
                    {s.cpu_limit}CPU · {s.memory_limit_mb}MB
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-200"><Time value={s.created_at} /></td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setToDelete(s);
                      }}
                      className="p-1.5 rounded text-slate-300 hover:text-red-400 hover:bg-red-900/20 transition-colors"
                      aria-label={`Delete session ${s.name || s.id.slice(0, 8)}`}
                      title="Delete session"
                    >
                      <Trash2 className="w-3.5 h-3.5" aria-hidden="true" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {visible.length > PAGE_SIZE && (
        <nav aria-label="Session pages" className="flex items-center justify-between text-xs text-slate-200">
          <span>
            Page {currentPage} of {pageCount}
          </span>
          <div className="flex gap-2">
            <button
              onClick={() => setPage(currentPage - 1)}
              disabled={currentPage <= 1}
              className="flex items-center gap-1 px-3 py-1.5 rounded border border-slate-700 hover:bg-slate-800 disabled:opacity-40"
            >
              <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" /> Previous
            </button>
            <button
              onClick={() => setPage(currentPage + 1)}
              disabled={currentPage >= pageCount}
              className="flex items-center gap-1 px-3 py-1.5 rounded border border-slate-700 hover:bg-slate-800 disabled:opacity-40"
            >
              Next <ChevronRight className="w-3.5 h-3.5" aria-hidden="true" />
            </button>
          </div>
        </nav>
      )}

      <ConfirmDialog
        open={toDelete !== null}
        title="Delete session?"
        message={
          <>
            <span className="font-mono">{toDelete?.name || toDelete?.id.slice(0, 8)}</span> and all of its files, runs and snapshots will be removed. This cannot be undone.
          </>
        }
        confirmLabel="Delete session"
        busyLabel="Deleting…"
        busy={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setToDelete(null)}
      />
    </div>
  );
}

function SortTh({ label, active, dir, onClick }: { label: string; active: boolean; dir: SortDir; onClick: () => void }) {
  return (
    <th scope="col" aria-sort={active ? (dir === "asc" ? "ascending" : "descending") : "none"} className="px-4 py-3 text-left font-medium">
      <button type="button" onClick={onClick} className="inline-flex items-center gap-1 uppercase tracking-wide hover:text-slate-200">
        {label}
        {active && (dir === "asc" ? <ArrowUp className="w-3 h-3" aria-hidden="true" /> : <ArrowDown className="w-3 h-3" aria-hidden="true" />)}
      </button>
    </th>
  );
}

function LabelChips({ labels, onPick }: { labels?: Record<string, string>; onPick: (kv: string) => void }) {
  const entries = Object.entries(labels ?? {});
  if (entries.length === 0) return null;
  return (
    <div className="mt-1 flex flex-wrap gap-1">
      {entries.map(([k, v]) => (
        <button
          key={k}
          type="button"
          onClick={() => onPick(`${k}=${v}`)}
          title={`Filter by ${k}=${v}`}
          className="inline-flex items-center gap-0.5 rounded bg-slate-800 px-1.5 py-0.5 font-mono text-[11px] text-slate-200 hover:bg-slate-700"
        >
          <span className="text-indigo-300">{k}</span>
          <span className="text-slate-300">=</span>
          {v}
        </button>
      ))}
    </div>
  );
}

function Field({
  label,
  error,
  children,
}: {
  label: string;
  error?: string;
  children: (id: string, describedBy?: string) => React.ReactNode;
}) {
  const id = useId();
  const errId = `${id}-err`;
  return (
    <div>
      <label htmlFor={id} className="block text-xs text-slate-200 mb-1.5">
        {label}
      </label>
      {children(id, error ? errId : undefined)}
      {error && (
        <p id={errId} className="mt-1 text-xs text-red-300">
          {error}
        </p>
      )}
    </div>
  );
}

const input = "w-full bg-slate-900 border border-slate-700 rounded-lg px-3 py-2 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-400 aria-[invalid=true]:border-red-500";
