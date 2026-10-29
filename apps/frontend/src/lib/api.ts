import type { Session, Run, File, AuditLog, AuditIntegrity, APIKey, CreatedKey, Pagination, PolicyStatus, PolicyEvalResult, Snapshot, SharedArtifact, Checkpoint, CostMetric, SessionCostSummary, CostBreakdown, CostAlert } from "@/types";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "";

function getApiKey(): string {
  return typeof window !== "undefined"
    ? localStorage.getItem("vaultrun_api_key") || ""
    : "";
}

// Keep a named export for components that need the raw key (e.g. streaming fetch)
export function getStoredApiKey(): string { return getApiKey(); }

/**
 * Stored in place of an API key when the user signed in with single sign-on
 * (VaultRun Enterprise). Requests then carry the HttpOnly session cookie the
 * API set, instead of a key header.
 */
export const SSO_SESSION = "sso-session";

/**
 * Credentials for a request: the API key header, or nothing for an SSO
 * session (the browser sends the cookie). Every request also says it comes
 * from the dashboard: the API requires that header before it accepts a
 * session cookie on a request that changes something, which a page on
 * another site cannot add without the API's consent (CSRF protection).
 */
export function authHeaders(key: string = getApiKey()): Record<string, string> {
  const h: Record<string, string> = { "X-VaultRun-Client": "dashboard" };
  if (key && key !== SSO_SESSION) h["X-API-Key"] = key;
  return h;
}

function getHeaders(): HeadersInit {
  return { "Content-Type": "application/json", ...authHeaders() };
}

/** Error from the API carrying the HTTP status so callers can tell 404 from 403 from 5xx. */
/** Fired when the API rejects the stored key; ApiKeyGate then returns to the sign-in form. */
export const UNAUTHORIZED_EVENT = "vaultrun:unauthorized";

export function notifyUnauthorized() {
  if (typeof window !== "undefined") window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
}

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const url = `${API_BASE}/api/v1${path}`;
  const resp = await fetch(url, {
    ...options,
    headers: { ...getHeaders(), ...(options.headers || {}) },
  });

  if (resp.status === 401) notifyUnauthorized();

  if (!resp.ok) {
    const body = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new ApiError(body.error || `HTTP ${resp.status}`, resp.status);
  }

  if (resp.status === 204) return undefined as T;
  return resp.json();
}

export interface AuditQuery {
  sessionId?: string;
  /** Action prefix, e.g. "apikey." */
  action?: string;
  /** YYYY-MM-DD, inclusive */
  from?: string;
  /** YYYY-MM-DD, exclusive */
  to?: string;
  /** Only the entry with this chain number. */
  seq?: number;
  /** Only the entry with this ID. */
  id?: string;
  /** "seq" lists in chain order instead of by time. */
  sort?: "time" | "seq";
  page?: number;
  limit?: number;
}

/** GET /api/v1/info (API 0.4.13 and later). */
export interface ServerInfo {
  version: string;
  edition: string;
  /** E.g. the license the Enterprise edition runs under; empty for Community. */
  edition_detail?: string;
  sandbox: { runtime: string; gvisor: boolean };
  evidence: { enabled: boolean; timestamped: boolean };
}

/** Single sign-on endpoints (VaultRun Enterprise). They live under /auth, next to /api. */
export const sso = {
  /** Which SSO providers the server offers; null when it offers none (or is not Enterprise). */
  providers: async (): Promise<{ oidc: boolean; saml: boolean } | null> => {
    try {
      const resp = await fetch(`${API_BASE}/auth/providers`, { credentials: "same-origin" });
      if (!resp.ok) return null;
      const body = (await resp.json()) as { oidc?: boolean; saml?: boolean };
      return body.oidc || body.saml ? { oidc: Boolean(body.oidc), saml: Boolean(body.saml) } : null;
    } catch {
      return null;
    }
  },
  /** True when the browser holds a valid SSO session cookie. */
  hasSession: async (): Promise<boolean> => {
    try {
      const resp = await fetch(`${API_BASE}/auth/me`, { headers: authHeaders(SSO_SESSION), credentials: "same-origin" });
      return resp.ok;
    } catch {
      return false;
    }
  },
  loginURL: (provider: "oidc" | "saml") => `${API_BASE}/auth/${provider}/login`,
  /** Ends the session on the server; throws when the server could not revoke it. */
  logout: async (): Promise<void> => {
    const resp = await fetch(`${API_BASE}/auth/logout`, {
      method: "POST",
      headers: authHeaders(SSO_SESSION),
      credentials: "same-origin",
    });
    if (!resp.ok && resp.status !== 401) {
      const body = await resp.json().catch(() => ({ error: resp.statusText }));
      throw new ApiError(body.error || `HTTP ${resp.status}`, resp.status);
    }
  },
  /** Issues a new personal API key for the signed-in user (shown once) and retires the previous one. */
  createApiKey: async (): Promise<{ api_key: string; prefix: string }> => {
    const resp = await fetch(`${API_BASE}/auth/api-key`, {
      method: "POST",
      headers: authHeaders(SSO_SESSION),
      credentials: "same-origin",
    });
    const body = await resp.json().catch(() => ({ error: resp.statusText }));
    if (!resp.ok) throw new ApiError(body.error || `HTTP ${resp.status}`, resp.status);
    return body;
  },
};

// Sessions
export const api = {
  /** null when the API predates /info. */
  info: () => request<ServerInfo>("/info").catch((e: unknown) => {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }),

  sessions: {
    list: (page = 1, limit = 20) =>
      request<{ sessions: Session[]; pagination: Pagination }>(
        `/sessions?page=${page}&limit=${limit}`
      ).then((r) => ({ ...r, sessions: r.sessions ?? [] })),

    get: (id: string) => request<Session>(`/sessions/${id}`),

    create: (body: {
      name?: string;
      image?: string;
      network_enabled?: boolean;
      cpu_limit?: number;
      memory_limit_mb?: number;
      timeout_seconds?: number;
      org_id?: string;
      replay_enabled?: boolean;
    }) => request<Session>("/sessions", { method: "POST", body: JSON.stringify(body) }),

    delete: (id: string) =>
      request<void>(`/sessions/${id}`, { method: "DELETE" }),

    /** The signed evidence pack (JSON), for `vaultrun evidence verify`. */
    evidence: (id: string) =>
      fetch(`${API_BASE}/api/v1/sessions/${id}/evidence`, { headers: authHeaders() }),

    updateLabels: (id: string, labels: Record<string, string>) =>
      request<{ labels: Record<string, string> }>(`/sessions/${id}/labels`, {
        method: "PATCH",
        body: JSON.stringify({ labels }),
      }),
  },

  runs: {
    list: (sessionId: string) =>
      request<{ runs: Run[] }>(`/sessions/${sessionId}/runs`).then(
        (r) => r.runs ?? []
      ),

    get: (id: string) => request<Run>(`/runs/${id}`),

    execute: (
      sessionId: string,
      body: {
        command: string;
        args?: string[];
        env?: Record<string, string>;
        working_dir?: string;
        timeout_seconds?: number;
      }
    ) =>
      request<Run>(`/sessions/${sessionId}/run`, {
        method: "POST",
        body: JSON.stringify(body),
      }),

    /** Submit a non-blocking async run. Returns immediately with run_id. */
    submitAsync: (
      sessionId: string,
      body: {
        command: string;
        args?: string[];
        env?: Record<string, string>;
        working_dir?: string;
        timeout_seconds?: number;
        callback_url?: string;
      }
    ) =>
      request<{ run_id: string; status: string; message: string }>(
        `/sessions/${sessionId}/run/async`,
        { method: "POST", body: JSON.stringify(body) }
      ),

    /**
     * Poll a run by ID until it reaches a terminal state (completed | failed | timeout).
     * Resolves with the final Run object. Rejects after maxAttempts or on error.
     */
    poll: async (runId: string, intervalMs = 1500, maxAttempts = 120): Promise<Run> => {
      const terminal = new Set(["completed", "failed", "timeout"]);
      for (let i = 0; i < maxAttempts; i++) {
        const run = await request<Run>(`/runs/${runId}`);
        if (terminal.has(run.status)) return run;
        await new Promise((r) => setTimeout(r, intervalMs));
      }
      throw new Error(`Run ${runId} did not complete within the polling window`);
    },
  },

  files: {
    list: (sessionId: string) =>
      request<{ files: File[] }>(`/sessions/${sessionId}/files`).then(
        (r) => r.files ?? []
      ),

    upload: async (sessionId: string, path: string, file: globalThis.File) => {
      const form = new FormData();
      form.append("file", file);
      form.append("path", path);

      const resp = await fetch(`${API_BASE}/api/v1/sessions/${sessionId}/files`, {
        method: "POST",
        headers: authHeaders(),
        body: form,
      });

      if (!resp.ok) {
        const body = await resp.json().catch(() => ({ error: resp.statusText }));
        throw new Error(body.error || `HTTP ${resp.status}`);
      }
      return resp.json() as Promise<File>;
    },

    download: (sessionId: string, path: string) =>
      fetch(`${API_BASE}/api/v1/sessions/${sessionId}/files/${path}`, {
        headers: authHeaders(),
      }),
  },

  audit: {
    list: (params: AuditQuery = {}) => {
      const qs = new URLSearchParams();
      if (params.sessionId) qs.set("session_id", params.sessionId);
      if (params.action) qs.set("action", params.action);
      if (params.from) qs.set("from", params.from);
      if (params.to) qs.set("to", params.to);
      if (params.seq) qs.set("seq", String(params.seq));
      if (params.id) qs.set("id", params.id);
      if (params.sort === "seq") qs.set("sort", "seq");
      qs.set("page", String(params.page ?? 1));
      qs.set("limit", String(params.limit ?? 50));
      return request<{ audit_logs: AuditLog[]; pagination: Pagination }>(`/audit?${qs}`).then((r) => ({
        logs: (r.audit_logs ?? []).map((l) => ({ ...l, metadata: l.metadata ?? {} })),
        total: r.pagination?.total ?? 0,
        page: r.pagination?.page ?? 1,
        limit: r.pagination?.limit ?? (params.limit ?? 50),
      }));
    },
    /** null when the API predates /audit/integrity. */
    integrity: (refresh = false) =>
      request<AuditIntegrity>(`/audit/integrity${refresh ? "?refresh=true" : ""}`).catch((e: unknown) => {
        if (e instanceof ApiError && (e.status === 404 || e.status === 405)) return null;
        throw e;
      }),
  },

  keys: {
    list: () =>
      request<{ api_keys: APIKey[] }>("/keys").then((r) => r.api_keys ?? []),

    create: (name: string, expiresAt?: string, allowedSecrets?: string[]) =>
      request<CreatedKey>("/keys", {
        method: "POST",
        body: JSON.stringify({
          name,
          ...(expiresAt ? { expires_at: expiresAt } : {}),
          ...(allowedSecrets ? { allowed_secrets: allowedSecrets } : {}),
        }),
      }),

    revoke: (id: string) =>
      request<void>(`/keys/${id}`, { method: "DELETE" }),

    /** Removes an already revoked key from the list (the API refuses active keys). */
    remove: (id: string) =>
      request<void>(`/keys/${id}?purge=true`, { method: "DELETE" }),
  },

  policy: {
    get: () => request<PolicyStatus>("/policy"),

    eval: (body: {
      type: "command" | "file";
      session_id?: string;
      command?: string;
      args?: string[];
      path?: string;
      write?: boolean;
    }) =>
      request<PolicyEvalResult>("/policy/eval", {
        method: "POST",
        body: JSON.stringify(body),
      }),
  },

  snapshots: {
    list: (sessionId: string) =>
      request<{ snapshots: Snapshot[] }>(`/sessions/${sessionId}/snapshots`).then(
        (r) => r.snapshots ?? []
      ),

    create: (sessionId: string, name: string) =>
      request<Snapshot>(`/sessions/${sessionId}/snapshots`, {
        method: "POST",
        body: JSON.stringify({ name }),
      }),

    delete: (snapshotId: string) =>
      request<void>(`/snapshots/${snapshotId}`, { method: "DELETE" }),

    download: (snapshotId: string) =>
      fetch(`${API_BASE}/api/v1/snapshots/${snapshotId}/download`, {
        headers: authHeaders(),
      }),
  },

  artifacts: {
    list: (page = 1, limit = 50) =>
      request<{ artifacts: SharedArtifact[]; pagination: Pagination }>(
        `/artifacts?page=${page}&limit=${limit}`
      ).then((r) => ({ ...r, artifacts: r.artifacts ?? [] })),

    promote: (sessionId: string, path: string, name?: string) =>
      request<SharedArtifact>(`/sessions/${sessionId}/artifacts`, {
        method: "POST",
        body: JSON.stringify(name ? { path, name } : { path }),
      }),

    delete: (id: string) =>
      request<void>(`/artifacts/${id}`, { method: "DELETE" }),

    /** The endpoint needs the API key header, so a plain <a href> would get a 401. */
    download: (id: string) =>
      fetch(`${API_BASE}/api/v1/artifacts/${id}/download`, {
        headers: authHeaders(),
      }),
  },

  checkpoints: {
    list: (sessionId: string) =>
      request<{ checkpoints: Checkpoint[] }>(`/sessions/${sessionId}/checkpoints`).then(
        (r) => r.checkpoints ?? []
      ),

    get: (sessionId: string, checkpointId: string) =>
      request<Checkpoint>(`/sessions/${sessionId}/checkpoints/${checkpointId}`),

    create: (sessionId: string, body: {
      run_id?: string;
      name?: string;
      description: string;
    }) =>
      request<Checkpoint>(`/sessions/${sessionId}/checkpoints`, {
        method: "POST",
        body: JSON.stringify(body),
      }),

    restore: (sessionId: string, checkpointId: string, stopActiveCommands = false) =>
      request<{ status: string }>(`/sessions/${sessionId}/checkpoints/${checkpointId}/restore`, {
        method: "POST",
        body: JSON.stringify({ stop_active_commands: stopActiveCommands }),
      }),

    fork: (checkpointId: string, name: string) =>
      request<{ session_id: string }>(`/checkpoints/${checkpointId}/fork`, {
        method: "POST",
        body: JSON.stringify({ name }),
      }),

    delete: (sessionId: string, checkpointId: string) =>
      request<void>(`/sessions/${sessionId}/checkpoints/${checkpointId}`, { method: "DELETE" }),
  },

  costs: {
    getSessionCosts: (sessionId: string) =>
      request<{ metrics: CostMetric[]; summary: SessionCostSummary }>(
        `/sessions/${sessionId}/costs`
      ),

    getBreakdown: (period?: string, limit = 100) => {
      const qs = `?limit=${limit}${period ? `&period=${period}` : ""}`;
      return request<CostBreakdown>(`/costs/breakdown${qs}`).then((b) => ({
        ...b,
        top_sessions: b.top_sessions ?? [],
      }));
    },

    getAlerts: (resolved = false) =>
      request<{ alerts: CostAlert[] }>(`/costs/alerts?resolved=${resolved}`).then(
        (r) => r.alerts ?? []
      ),

    resolveAlert: (alertId: string) =>
      request<{ status: string }>(`/costs/alerts/${alertId}/resolve`, { method: "POST" }),
  },

  orgs: {
    mine: () =>
      request<{ orgs: { id: string; name: string; slug: string; role: string }[] }>("/me/orgs"),
  },
};

