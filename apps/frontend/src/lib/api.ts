import type { Session, Run, File, AuditLog, APIKey, CreatedKey, Pagination, PolicyStatus, PolicyEvalResult } from "@/types";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "";

function getApiKey(): string {
  return typeof window !== "undefined"
    ? localStorage.getItem("vaultrun_api_key") || ""
    : "";
}

// Keep a named export for components that need the raw key (e.g. streaming fetch)
export function getStoredApiKey(): string { return getApiKey(); }

function getHeaders(): HeadersInit {
  return {
    "Content-Type": "application/json",
    "X-API-Key": getApiKey(),
  };
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const url = `${API_BASE}/api/v1${path}`;
  const resp = await fetch(url, {
    ...options,
    headers: { ...getHeaders(), ...(options.headers || {}) },
  });

  if (!resp.ok) {
    const body = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(body.error || `HTTP ${resp.status}`);
  }

  if (resp.status === 204) return undefined as T;
  return resp.json();
}

// Sessions
export const api = {
  sessions: {
    list: (page = 1, limit = 20) =>
      request<{ sessions: Session[]; pagination: Pagination }>(
        `/sessions?page=${page}&limit=${limit}`
      ),

    get: (id: string) => request<Session>(`/sessions/${id}`),

    create: (body: {
      name?: string;
      image?: string;
      network_enabled?: boolean;
      cpu_limit?: number;
      memory_limit_mb?: number;
      timeout_seconds?: number;
    }) => request<Session>("/sessions", { method: "POST", body: JSON.stringify(body) }),

    delete: (id: string) =>
      request<void>(`/sessions/${id}`, { method: "DELETE" }),
  },

  runs: {
    list: (sessionId: string) =>
      request<{ runs: Run[] }>(`/sessions/${sessionId}/runs`).then(
        (r) => r.runs
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
  },

  files: {
    list: (sessionId: string) =>
      request<{ files: File[] }>(`/sessions/${sessionId}/files`).then(
        (r) => r.files
      ),

    upload: async (sessionId: string, path: string, file: globalThis.File) => {
      const form = new FormData();
      form.append("file", file);
      form.append("path", path);

      const resp = await fetch(`${API_BASE}/api/v1/sessions/${sessionId}/files`, {
        method: "POST",
        headers: { "X-API-Key": getApiKey() },
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
        headers: { "X-API-Key": getApiKey() },
      }),
  },

  audit: {
    list: (sessionId?: string) => {
      const qs = sessionId ? `?session_id=${sessionId}` : "";
      return request<{ audit_logs: AuditLog[] }>(`/audit${qs}`).then(
        (r) => r.audit_logs
      );
    },
  },

  keys: {
    list: () =>
      request<{ api_keys: APIKey[] }>("/keys").then((r) => r.api_keys),

    create: (name: string, expiresAt?: string) =>
      request<CreatedKey>("/keys", {
        method: "POST",
        body: JSON.stringify(expiresAt ? { name, expires_at: expiresAt } : { name }),
      }),

    revoke: (id: string) =>
      request<void>(`/keys/${id}`, { method: "DELETE" }),
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
};

