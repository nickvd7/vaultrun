export interface Session {
  id: string;
  name?: string;
  image: string;
  status: "created" | "running" | "stopped" | "error";
  container_id?: string;
  network_enabled: boolean;
  cpu_limit: number;
  memory_limit_mb: number;
  timeout_seconds: number;
  labels: Record<string, string>;
  allowed_hosts?: string[];
  created_by: string;
  created_at: string;
  updated_at: string;
  stopped_at?: string;
}

export interface Run {
  id: string;
  session_id: string;
  command: string;
  args: string[];
  status: "pending" | "running" | "completed" | "failed" | "timeout";
  exit_code?: number;
  stdout?: string;
  stderr?: string;
  duration_ms?: number;
  timeout_seconds: number;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface File {
  id: string;
  session_id: string;
  path: string;
  size_bytes: number;
  content_type: string;
  created_at: string;
  updated_at: string;
}

export interface AuditLog {
  id: string;
  timestamp: string;
  actor: string;
  session_id?: string;
  run_id?: string;
  action: string;
  metadata: Record<string, unknown>;
}

export interface Pagination {
  limit: number;
  offset: number;
  page: number;
}

export interface APIKey {
  id: string;
  name: string;
  prefix: string;
  active: boolean;
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
}

// Returned once on creation — includes the plaintext key
export interface CreatedKey extends APIKey {
  key: string;
}

export interface PolicyStatus {
  enabled: boolean;
  file_path?: string;
  content?: string;
  error?: string;
}

export interface PolicyEvalResult {
  allowed: boolean;
  type: string;
  reason?: string;
}
