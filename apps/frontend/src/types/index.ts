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
  /** Name of the API key that created the session (API 0.4.7 and later). */
  created_by_name?: string;
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
  checkpoint_id?: string;
  restored_from_checkpoint_id?: string;
}

export interface File {
  id: string;
  session_id: string;
  path: string;
  size_bytes: number;
  content_type: string;
  /** True when the workspace entry is a symlink, not a regular file. */
  symlink?: boolean;
  created_at: string;
  updated_at: string;
}

export interface AuditLog {
  id: string;
  timestamp: string;
  actor: string;
  /** Name of the API key behind actor (API 0.4.7 and later); absent for deleted keys. */
  actor_name?: string;
  session_id?: string;
  run_id?: string;
  action: string;
  metadata: Record<string, unknown>;
  /** Present when the server signs audit entries. */
  sig_valid?: boolean;
  /**
   * valid: signature matches. invalid: altered, or signature removed.
   * unsigned / unverifiable: written before verifiable signing began.
   * Absent on API versions before 0.4.4.
   */
  sig_status?: "valid" | "invalid" | "unsigned" | "unverifiable";
  /** Why an entry is invalid (API 0.4.5 and later). */
  sig_reason?: string;
  /** Position in the hash chain; absent on entries written before chaining. */
  seq?: number;
  /** altered: this entry was changed. chain: it is intact but the entry before it was changed or deleted. */
  sig_kind?: "altered" | "chain";
  /** Full-sentence explanation of sig_reason (API 0.4.6 and later). */
  sig_detail?: string;
  /** Dated earlier than the entry before it in the chain (API 0.4.6 and later). */
  out_of_order?: boolean;
}

/** One entry named by an integrity warning (API 0.4.6 and later). */
export interface AuditIntegrityEntry {
  id: string;
  seq?: number;
  kind: "altered" | "chain";
  reason: string;
  detail: string;
  out_of_order?: boolean;
}

/** Verdict over the whole audit log, from GET /audit/integrity (API 0.4.5 and later). */
export interface AuditIntegrity {
  status: "ok" | "compromised" | "not_started" | "disabled";
  checked_at: string;
  chained_entries: number;
  legacy_entries: number;
  invalid_entries: number;
  /** Entries that were changed themselves (API 0.4.6 and later). */
  altered_entries?: number;
  /** Intact entries that follow a changed one (API 0.4.6 and later). */
  affected_entries?: number;
  missing_entries: number;
  problems: string[];
  /** "all" for the master key; "own" when other keys' entries are only counted. */
  scope?: "all" | "own";
  own_invalid_entries?: number;
  other_invalid_entries?: number;
  /** Master key: every affected entry (first 25). Other keys: their own. */
  entries?: AuditIntegrityEntry[];
  /** Master key only. */
  invalid_ids?: string[];
  missing_seqs?: string[];
}

export interface Pagination {
  limit: number;
  offset: number;
  page: number;
  total?: number;
}

export interface APIKey {
  id: string;
  name: string;
  prefix: string;
  active: boolean;
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
  /** null/absent = may request any secret; a list restricts the key. */
  allowed_secrets?: string[] | null;
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
  created_by_name?: string;
  created_at: string;
}

export interface SharedArtifact {
  id: string;
  name: string;
  content_type: string;
  size_bytes: number;
  session_id?: string;
  created_by: string;
  created_by_name?: string;
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
  /** Set when the session has ended (deleted, or stopped when idle). */
  stopped_at?: string;
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
  /** Sessions with cost in the period, which can exceed top_sessions.length (API 0.4.7 and later). */
  total_sessions?: number;
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
