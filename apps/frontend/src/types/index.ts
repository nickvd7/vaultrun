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
  org_id?: string;

  /** Checkpoints are only recorded when this is true. */
  replay_enabled: boolean;
  forked_from_checkpoint_id?: string;

  /** Running total; the per-period records live in cost_metrics. */
  total_cost?: number;
  last_cost_update?: string;

  /** Set when the session was created from a marketplace template. */
  template_id?: string;

  max_agents?: number;
  allow_collaboration?: boolean;
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

export interface Snapshot {
  id: string;
  session_id: string;
  name: string;
  size_bytes: number;
  created_by: string;
  created_at: string;
}

export interface SharedArtifact {
  id: string;
  name: string;
  content_type: string;
  size_bytes: number;
  session_id?: string;
  created_by: string;
  created_at: string;
}

export interface Checkpoint {
  id: string;
  session_id: string;
  run_id?: string;
  checkpoint_number: number;
  name?: string;
  description: string;
  workspace_snapshot_id: string;
  env_vars_snapshot?: Record<string, string>;
  command?: string;
  args?: Record<string, string>;
  exit_code?: number;
  duration_ms?: number;
  stdout_preview?: string;
  stderr_preview?: string;
  signature: string;
  created_at: string;
  size_bytes: number;
}

export interface CostMetric {
  id: string;
  session_id: string;
  period_start: string;
  period_end: string;
  cpu_core_hours: number;
  memory_gb_hours: number;
  gpu_hours: number;
  workspace_gb_days: number;
  snapshot_gb_days: number;
  artifact_gb_days: number;
  egress_gb: number;
  ingress_gb: number;
  compute_cost: number;
  storage_cost: number;
  network_cost: number;
  total_cost: number;
  created_at: string;
}

export interface SessionCostSummary {
  session_id: string;
  session_name: string;
  compute_cost: number;
  storage_cost: number;
  network_cost: number;
  total_cost: number;
  first_metric: string;
  last_metric: string;
}

export interface CostBreakdown {
  period: string;
  compute_cost: number;
  storage_cost: number;
  network_cost: number;
  total_cost: number;
  top_sessions: SessionCostSummary[];
  alert_count: number;
}

export interface CostAlert {
  id: string;
  alert_type: string;
  severity: string;
  session_id?: string;
  org_id?: string;
  title: string;
  description: string;
  potential_savings?: number;
  resolved: boolean;
  created_at: string;
}
