package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/models"
)

// Sessions

func CreateSession(ctx context.Context, db *sqlx.DB, s *models.Session) error {
	q := `
		INSERT INTO sessions (id, name, image, status, network_enabled, cpu_limit,
		    memory_limit_mb, timeout_seconds, workspace_path, labels, allowed_hosts,
		    created_by, org_id, created_at, updated_at)
		VALUES (:id, :name, :image, :status, :network_enabled, :cpu_limit,
		    :memory_limit_mb, :timeout_seconds, :workspace_path, :labels, :allowed_hosts,
		    :created_by, :org_id, :created_at, :updated_at)
	`
	_, err := db.NamedExecContext(ctx, q, s)
	return err
}

func GetSession(ctx context.Context, db *sqlx.DB, id uuid.UUID) (*models.Session, error) {
	var s models.Session
	err := db.GetContext(ctx, &s, `SELECT * FROM sessions WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSessions returns active sessions. When actor is non-empty only that
// actor's sessions are returned (tenant isolation). Master key passes "".
// labelKey/labelValue optionally filter by a specific label (both must be set).
// Initialises the slice to non-nil so JSON always encodes [] instead of null.
func ListSessions(ctx context.Context, db *sqlx.DB, actor, labelKey, labelValue string, limit, offset int) ([]*models.Session, error) {
	sessions := make([]*models.Session, 0)
	var err error
	switch {
	case actor != "" && labelKey != "":
		err = db.SelectContext(ctx, &sessions,
			`SELECT * FROM sessions WHERE stopped_at IS NULL AND created_by = $1
			   AND labels @> jsonb_build_object($2::text, $3::text)
			 ORDER BY created_at DESC LIMIT $4 OFFSET $5`,
			actor, labelKey, labelValue, limit, offset,
		)
	case actor != "":
		err = db.SelectContext(ctx, &sessions,
			`SELECT * FROM sessions WHERE stopped_at IS NULL AND created_by = $1
			 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
			actor, limit, offset,
		)
	case labelKey != "":
		err = db.SelectContext(ctx, &sessions,
			`SELECT * FROM sessions WHERE stopped_at IS NULL
			   AND labels @> jsonb_build_object($1::text, $2::text)
			 ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
			labelKey, labelValue, limit, offset,
		)
	default:
		err = db.SelectContext(ctx, &sessions,
			`SELECT * FROM sessions WHERE stopped_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
			limit, offset,
		)
	}
	return sessions, err
}

// UpdateSessionLabels replaces the labels map for a session.
func UpdateSessionLabels(ctx context.Context, db *sqlx.DB, id uuid.UUID, labels models.JSONB) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET labels = $1, updated_at = NOW() WHERE id = $2`,
		labels, id,
	)
	return err
}

func UpdateSessionStatus(ctx context.Context, db *sqlx.DB, id uuid.UUID, status string, containerID *string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET status = $1, container_id = $2, updated_at = NOW() WHERE id = $3`,
		status, containerID, id,
	)
	return err
}

func StopSession(ctx context.Context, db *sqlx.DB, id uuid.UUID) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET status = $1, stopped_at = NOW(), updated_at = NOW() WHERE id = $2`,
		models.SessionStatusStopped, id,
	)
	return err
}

// Runs

func CreateRun(ctx context.Context, db *sqlx.DB, r *models.Run) error {
	q := `
		INSERT INTO runs (id, session_id, command, args, env, working_dir, status,
		    timeout_seconds, callback_url, created_at, updated_at)
		VALUES (:id, :session_id, :command, :args, :env, :working_dir, :status,
		    :timeout_seconds, :callback_url, :created_at, :updated_at)
	`
	_, err := db.NamedExecContext(ctx, q, r)
	return err
}

func GetRun(ctx context.Context, db *sqlx.DB, id uuid.UUID) (*models.Run, error) {
	var r models.Run
	err := db.GetContext(ctx, &r, `SELECT * FROM runs WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func ListRuns(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID, limit, offset int) ([]*models.Run, error) {
	runs := make([]*models.Run, 0)
	err := db.SelectContext(ctx, &runs,
		`SELECT * FROM runs WHERE session_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		sessionID, limit, offset,
	)
	return runs, err
}

type UpdateRunParams struct {
	ID         uuid.UUID
	Status     string
	ExitCode   *int
	Stdout     *string
	Stderr     *string
	DurationMS *int64
	StartedAt  *time.Time
	FinishedAt *time.Time
}

func UpdateRun(ctx context.Context, db *sqlx.DB, p UpdateRunParams) error {
	_, err := db.ExecContext(ctx, `
		UPDATE runs
		SET status      = $1,
		    exit_code   = $2,
		    stdout      = $3,
		    stderr      = $4,
		    duration_ms = $5,
		    started_at  = $6,
		    finished_at = $7,
		    updated_at  = NOW()
		WHERE id = $8
	`, p.Status, p.ExitCode, p.Stdout, p.Stderr, p.DurationMS, p.StartedAt, p.FinishedAt, p.ID)
	return err
}

// Files

func UpsertFile(ctx context.Context, db *sqlx.DB, f *models.File) error {
	q := `
		INSERT INTO files (id, session_id, path, size_bytes, content_type, created_at, updated_at)
		VALUES (:id, :session_id, :path, :size_bytes, :content_type, :created_at, :updated_at)
		ON CONFLICT (session_id, path) DO UPDATE
		    SET size_bytes   = EXCLUDED.size_bytes,
		        content_type = EXCLUDED.content_type,
		        updated_at   = EXCLUDED.updated_at
	`
	_, err := db.NamedExecContext(ctx, q, f)
	return err
}

func GetFile(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID, path string) (*models.File, error) {
	var f models.File
	err := db.GetContext(ctx, &f, `SELECT * FROM files WHERE session_id = $1 AND path = $2`, sessionID, path)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func ListFiles(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID) ([]*models.File, error) {
	files := make([]*models.File, 0)
	err := db.SelectContext(ctx, &files,
		`SELECT * FROM files WHERE session_id = $1 ORDER BY path ASC`,
		sessionID,
	)
	return files, err
}

func DeleteFile(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID, path string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM files WHERE session_id = $1 AND path = $2`, sessionID, path)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Audit logs

func CreateAuditLog(ctx context.Context, db *sqlx.DB, a *models.AuditLog) error {
	q := `
		INSERT INTO audit_logs (id, timestamp, actor, session_id, run_id, action, metadata, sig)
		VALUES (:id, :timestamp, :actor, :session_id, :run_id, :action, :metadata, :sig)
	`
	_, err := db.NamedExecContext(ctx, q, a)
	return err
}

// ListAuditLogs returns audit log entries. When actor is non-empty only
// that actor's log entries are returned (tenant isolation). When sessionID
// is set it takes precedence. Master key passes actor="".
// Initialises the slice to non-nil so JSON always encodes [] instead of null.
func ListAuditLogs(ctx context.Context, db *sqlx.DB, sessionID *uuid.UUID, actor string, limit, offset int) ([]*models.AuditLog, error) {
	logs := make([]*models.AuditLog, 0)
	if sessionID != nil {
		err := db.SelectContext(ctx, &logs,
			`SELECT * FROM audit_logs WHERE session_id = $1 ORDER BY timestamp DESC LIMIT $2 OFFSET $3`,
			sessionID, limit, offset,
		)
		return logs, err
	}
	if actor != "" {
		err := db.SelectContext(ctx, &logs,
			`SELECT * FROM audit_logs WHERE actor = $1 ORDER BY timestamp DESC LIMIT $2 OFFSET $3`,
			actor, limit, offset,
		)
		return logs, err
	}
	err := db.SelectContext(ctx, &logs,
		`SELECT * FROM audit_logs ORDER BY timestamp DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	return logs, err
}

// API Keys

func CreateAPIKey(ctx context.Context, db *sqlx.DB, k *models.APIKey) error {
	q := `
		INSERT INTO api_keys (id, name, key_hash, prefix, created_at, expires_at, active)
		VALUES (:id, :name, :key_hash, :prefix, :created_at, :expires_at, :active)
	`
	_, err := db.NamedExecContext(ctx, q, k)
	return err
}

func GetAPIKeyByHash(ctx context.Context, db *sqlx.DB, keyHash string) (*models.APIKey, error) {
	var k models.APIKey
	err := db.GetContext(ctx, &k,
		`SELECT * FROM api_keys
		 WHERE key_hash = $1
		   AND active = TRUE
		   AND (expires_at IS NULL OR expires_at > NOW())`,
		keyHash)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func UpdateAPIKeyLastUsed(ctx context.Context, db *sqlx.DB, id uuid.UUID) error {
	_, err := db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, id)
	return err
}

func ListAPIKeys(ctx context.Context, db *sqlx.DB) ([]*models.APIKey, error) {
	keys := make([]*models.APIKey, 0)
	err := db.SelectContext(ctx, &keys, `SELECT * FROM api_keys ORDER BY created_at DESC`)
	return keys, err
}

func RevokeAPIKey(ctx context.Context, db *sqlx.DB, id uuid.UUID) error {
	// No active=TRUE filter: makes revocation idempotent (revoking an already-
	// revoked key returns success rather than 404, preventing oracle attacks).
	// We still return ErrNoRows if the key never existed at all.
	//
	// revoked_at is set only when transitioning from active=TRUE so that the
	// timestamp records when the key was first revoked (not re-revoked).
	res, err := db.ExecContext(ctx,
		`UPDATE api_keys
		    SET active = FALSE,
		        revoked_at = COALESCE(revoked_at, NOW())
		  WHERE id = $1`,
		id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListActiveSessions returns sessions that are still running (for cleanup jobs).
func ListActiveSessions(ctx context.Context, db *sqlx.DB) ([]*models.Session, error) {
	var sessions []*models.Session
	err := db.SelectContext(ctx, &sessions,
		`SELECT * FROM sessions WHERE status IN ($1, $2) AND stopped_at IS NULL`,
		models.SessionStatusCreated, models.SessionStatusRunning,
	)
	return sessions, err
}

// CountActiveSessionsByActor returns the number of active (not stopped) sessions
// for a given actor. Used to enforce per-actor session limits.
func CountActiveSessionsByActor(ctx context.Context, db *sqlx.DB, actor string) (int, error) {
	var n int
	err := db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM sessions WHERE created_by = $1 AND stopped_at IS NULL`,
		actor,
	)
	return n, err
}

func CountSessions(ctx context.Context, db *sqlx.DB) (int, error) {
	var n int
	err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM sessions WHERE stopped_at IS NULL`)
	return n, err
}

// CountSessionsFiltered returns the number of active sessions matching the same
// filters as ListSessions (actor + optional label key/value).
func CountSessionsFiltered(ctx context.Context, db *sqlx.DB, actor, labelKey, labelValue string) (int, error) {
	var n int
	var err error
	switch {
	case actor != "" && labelKey != "":
		err = db.GetContext(ctx, &n,
			`SELECT COUNT(*) FROM sessions WHERE stopped_at IS NULL AND created_by = $1
			   AND labels @> jsonb_build_object($2::text, $3::text)`,
			actor, labelKey, labelValue)
	case actor != "":
		err = db.GetContext(ctx, &n,
			`SELECT COUNT(*) FROM sessions WHERE stopped_at IS NULL AND created_by = $1`, actor)
	case labelKey != "":
		err = db.GetContext(ctx, &n,
			`SELECT COUNT(*) FROM sessions WHERE stopped_at IS NULL
			   AND labels @> jsonb_build_object($1::text, $2::text)`,
			labelKey, labelValue)
	default:
		err = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM sessions WHERE stopped_at IS NULL`)
	}
	return n, err
}

func CountRuns(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID) (int, error) {
	var n int
	err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM runs WHERE session_id = $1`, sessionID)
	return n, err
}

func CountRunsGlobal(ctx context.Context, db *sqlx.DB) (int, error) {
	var n int
	err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM runs`)
	return n, err
}

func CountFiles(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID) (int, error) {
	var n int
	err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM files WHERE session_id = $1`, sessionID)
	return n, err
}

// SumWorkspaceBytes returns the total size in bytes of all files tracked for
// the given session. Used by the upload handler to enforce MAX_WORKSPACE_MB.
func SumWorkspaceBytes(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID) (int64, error) {
	var total int64
	err := db.GetContext(ctx, &total,
		`SELECT COALESCE(SUM(size_bytes), 0) FROM files WHERE session_id = $1`, sessionID)
	return total, err
}

// CountAuditLogs mirrors the filter logic of ListAuditLogs for accurate totals.
func CountAuditLogs(ctx context.Context, db *sqlx.DB, sessionID *uuid.UUID, actor string) (int, error) {
	var n int
	var err error
	if sessionID != nil {
		err = db.GetContext(ctx, &n,
			`SELECT COUNT(*) FROM audit_logs WHERE session_id = $1`, sessionID)
	} else if actor != "" {
		err = db.GetContext(ctx, &n,
			`SELECT COUNT(*) FROM audit_logs WHERE actor = $1`, actor)
	} else {
		err = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM audit_logs`)
	}
	return n, err
}

func CountAPIKeys(ctx context.Context, db *sqlx.DB) (int, error) {
	var n int
	err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM api_keys`)
	return n, err
}

// ListAuditSince returns up to limit audit entries created after since, ordered by timestamp ASC.
func ListAuditSince(ctx context.Context, db *sqlx.DB, since time.Time, limit int) ([]models.AuditLog, error) {
	var entries []models.AuditLog
	err := db.SelectContext(ctx, &entries,
		`SELECT id, timestamp, actor, session_id, run_id, action, metadata
		   FROM audit_logs
		  WHERE timestamp > $1
		  ORDER BY timestamp ASC
		  LIMIT $2`,
		since, limit)
	return entries, err
}

// DeleteOldAuditLogs removes audit log entries whose timestamp is older than
// the given cutoff. Returns the number of rows deleted.
// Called by the background cleanup goroutine when AUDIT_LOG_RETENTION_DAYS > 0.
func DeleteOldAuditLogs(ctx context.Context, db *sqlx.DB, before time.Time) (int64, error) {
	result, err := db.ExecContext(ctx,
		`DELETE FROM audit_logs WHERE timestamp < $1`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// FailStalePendingRuns marks as "failed" any runs that are still in the
// "pending" state and were created before the given cutoff timestamp.
//
// This is called at startup to clean up runs that were mid-flight when the
// previous server instance was killed (e.g., a crash or SIGKILL during an
// in-memory queue drain). Without this cleanup, such runs sit in "pending"
// forever and are never surfaced to callers as failures.
//
// For the Redis Streams backend, the reaper goroutine handles re-delivery;
// this function is a last-resort safety net for both queue backends.
func FailStalePendingRuns(ctx context.Context, db *sqlx.DB, before time.Time) (int64, error) {
	result, err := db.ExecContext(ctx, `
		UPDATE runs
		SET    status     = 'failed',
		       updated_at = NOW()
		WHERE  status     = 'pending'
		AND    created_at < $1
	`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Organizations

func CreateOrg(ctx context.Context, db *sqlx.DB, o *models.Organization) error {
	_, err := db.NamedExecContext(ctx, `
		INSERT INTO organizations (id, name, slug, created_at, updated_at)
		VALUES (:id, :name, :slug, :created_at, :updated_at)
	`, o)
	return err
}

func GetOrg(ctx context.Context, db *sqlx.DB, id uuid.UUID) (*models.Organization, error) {
	var o models.Organization
	err := db.GetContext(ctx, &o, `SELECT * FROM organizations WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func GetOrgBySlug(ctx context.Context, db *sqlx.DB, slug string) (*models.Organization, error) {
	var o models.Organization
	err := db.GetContext(ctx, &o, `SELECT * FROM organizations WHERE slug = $1`, slug)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func ListOrgs(ctx context.Context, db *sqlx.DB, limit, offset int) ([]*models.Organization, error) {
	orgs := make([]*models.Organization, 0)
	err := db.SelectContext(ctx, &orgs,
		`SELECT * FROM organizations ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	return orgs, err
}

func CountOrgs(ctx context.Context, db *sqlx.DB) (int, error) {
	var n int
	err := db.GetContext(ctx, &n, `SELECT COUNT(*) FROM organizations`)
	return n, err
}

func DeleteOrg(ctx context.Context, db *sqlx.DB, id uuid.UUID) error {
	res, err := db.ExecContext(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Org members

func AddOrgMember(ctx context.Context, db *sqlx.DB, m *models.OrgMember) error {
	_, err := db.NamedExecContext(ctx, `
		INSERT INTO org_members (org_id, principal, role, created_at)
		VALUES (:org_id, :principal, :role, :created_at)
		ON CONFLICT (org_id, principal) DO UPDATE SET role = EXCLUDED.role
	`, m)
	return err
}

func GetOrgMember(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, principal string) (*models.OrgMember, error) {
	var m models.OrgMember
	err := db.GetContext(ctx, &m,
		`SELECT * FROM org_members WHERE org_id = $1 AND principal = $2`,
		orgID, principal,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetOrgMemberRole returns the role for the given actor in the given org,
// or sql.ErrNoRows if they are not a member.
func GetOrgMemberRole(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, principal string) (string, error) {
	var role string
	err := db.GetContext(ctx, &role,
		`SELECT role FROM org_members WHERE org_id = $1 AND principal = $2`,
		orgID, principal,
	)
	return role, err
}

func ListOrgMembers(ctx context.Context, db *sqlx.DB, orgID uuid.UUID) ([]*models.OrgMember, error) {
	members := make([]*models.OrgMember, 0)
	err := db.SelectContext(ctx, &members,
		`SELECT * FROM org_members WHERE org_id = $1 ORDER BY created_at ASC`,
		orgID,
	)
	return members, err
}

func RemoveOrgMember(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, principal string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM org_members WHERE org_id = $1 AND principal = $2`,
		orgID, principal,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetActorOrgs returns all org memberships for the given actor.
func GetActorOrgs(ctx context.Context, db *sqlx.DB, principal string) ([]*models.OrgMember, error) {
	members := make([]*models.OrgMember, 0)
	err := db.SelectContext(ctx, &members,
		`SELECT * FROM org_members WHERE principal = $1`,
		principal,
	)
	return members, err
}

// ListOrgSessions returns active sessions belonging to the given org.
func ListOrgSessions(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, limit, offset int) ([]*models.Session, error) {
	sessions := make([]*models.Session, 0)
	err := db.SelectContext(ctx, &sessions,
		`SELECT * FROM sessions WHERE stopped_at IS NULL AND org_id = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	return sessions, err
}

func CountOrgSessions(ctx context.Context, db *sqlx.DB, orgID uuid.UUID) (int, error) {
	var n int
	err := db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM sessions WHERE stopped_at IS NULL AND org_id = $1`, orgID)
	return n, err
}

// ListSessionsForActor returns all active sessions visible to the actor:
// own sessions UNION sessions in any org the actor belongs to.
// When actor is empty (master), all sessions are returned.
func ListSessionsForActor(ctx context.Context, db *sqlx.DB, actor, labelKey, labelValue string, limit, offset int) ([]*models.Session, error) {
	sessions := make([]*models.Session, 0)
	if actor == "" {
		// master: existing ListSessions logic handles this
		return ListSessions(ctx, db, "", labelKey, labelValue, limit, offset)
	}
	if labelKey != "" {
		err := db.SelectContext(ctx, &sessions, `
			SELECT DISTINCT s.*
			FROM sessions s
			LEFT JOIN org_members om ON om.org_id = s.org_id AND om.principal = $1
			WHERE s.stopped_at IS NULL
			  AND (s.created_by = $1 OR om.principal IS NOT NULL)
			  AND s.labels @> jsonb_build_object($2::text, $3::text)
			ORDER BY s.created_at DESC
			LIMIT $4 OFFSET $5
		`, actor, labelKey, labelValue, limit, offset)
		return sessions, err
	}
	err := db.SelectContext(ctx, &sessions, `
		SELECT DISTINCT s.*
		FROM sessions s
		LEFT JOIN org_members om ON om.org_id = s.org_id AND om.principal = $1
		WHERE s.stopped_at IS NULL
		  AND (s.created_by = $1 OR om.principal IS NOT NULL)
		ORDER BY s.created_at DESC
		LIMIT $2 OFFSET $3
	`, actor, limit, offset)
	return sessions, err
}

// CountSessionsForActor mirrors ListSessionsForActor for pagination totals.
func CountSessionsForActor(ctx context.Context, db *sqlx.DB, actor, labelKey, labelValue string) (int, error) {
	var n int
	if actor == "" {
		return CountSessionsFiltered(ctx, db, "", labelKey, labelValue)
	}
	if labelKey != "" {
		err := db.GetContext(ctx, &n, `
			SELECT COUNT(DISTINCT s.id)
			FROM sessions s
			LEFT JOIN org_members om ON om.org_id = s.org_id AND om.principal = $1
			WHERE s.stopped_at IS NULL
			  AND (s.created_by = $1 OR om.principal IS NOT NULL)
			  AND s.labels @> jsonb_build_object($2::text, $3::text)
		`, actor, labelKey, labelValue)
		return n, err
	}
	err := db.GetContext(ctx, &n, `
		SELECT COUNT(DISTINCT s.id)
		FROM sessions s
		LEFT JOIN org_members om ON om.org_id = s.org_id AND om.principal = $1
		WHERE s.stopped_at IS NULL
		  AND (s.created_by = $1 OR om.principal IS NOT NULL)
	`, actor)
	return n, err
}

// ─── Snapshots ────────────────────────────────────────────────────────────────

func CreateSnapshot(ctx context.Context, db *sqlx.DB, s *models.Snapshot) error {
	_, err := db.NamedExecContext(ctx, `
		INSERT INTO session_snapshots (id, session_id, name, created_by, size_bytes, archive_path, created_at)
		VALUES (:id, :session_id, :name, :created_by, :size_bytes, :archive_path, :created_at)
	`, s)
	return err
}

func GetSnapshot(ctx context.Context, db *sqlx.DB, id uuid.UUID) (*models.Snapshot, error) {
	var s models.Snapshot
	err := db.GetContext(ctx, &s, `SELECT * FROM session_snapshots WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func ListSnapshots(ctx context.Context, db *sqlx.DB, sessionID uuid.UUID) ([]*models.Snapshot, error) {
	snaps := make([]*models.Snapshot, 0)
	err := db.SelectContext(ctx, &snaps,
		`SELECT * FROM session_snapshots WHERE session_id = $1 ORDER BY created_at DESC`,
		sessionID,
	)
	return snaps, err
}

func DeleteSnapshot(ctx context.Context, db *sqlx.DB, id uuid.UUID) (string, error) {
	var archivePath string
	err := db.GetContext(ctx, &archivePath,
		`DELETE FROM session_snapshots WHERE id = $1 RETURNING archive_path`, id)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	return archivePath, err
}

// ─── Shared artifacts ─────────────────────────────────────────────────────────

func CreateArtifact(ctx context.Context, db *sqlx.DB, a *models.SharedArtifact) error {
	_, err := db.NamedExecContext(ctx, `
		INSERT INTO shared_artifacts (id, name, artifact_path, size_bytes, content_type, created_by, session_id, created_at)
		VALUES (:id, :name, :artifact_path, :size_bytes, :content_type, :created_by, :session_id, :created_at)
	`, a)
	return err
}

func GetArtifact(ctx context.Context, db *sqlx.DB, id uuid.UUID) (*models.SharedArtifact, error) {
	var a models.SharedArtifact
	err := db.GetContext(ctx, &a, `SELECT * FROM shared_artifacts WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func ListArtifacts(ctx context.Context, db *sqlx.DB, actor string, limit, offset int) ([]*models.SharedArtifact, error) {
	artifacts := make([]*models.SharedArtifact, 0)
	if actor != "" {
		err := db.SelectContext(ctx, &artifacts,
			`SELECT * FROM shared_artifacts WHERE created_by = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
			actor, limit, offset,
		)
		return artifacts, err
	}
	err := db.SelectContext(ctx, &artifacts,
		`SELECT * FROM shared_artifacts ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	return artifacts, err
}

func CountArtifacts(ctx context.Context, db *sqlx.DB, actor string) (int, error) {
	var n int
	var err error
	if actor != "" {
		err = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM shared_artifacts WHERE created_by = $1`, actor)
	} else {
		err = db.GetContext(ctx, &n, `SELECT COUNT(*) FROM shared_artifacts`)
	}
	return n, err
}

// TotalArtifactBytes returns the sum of size_bytes for all artifacts owned by actor.
// Pass an empty actor to sum across all actors (master use).
func TotalArtifactBytes(ctx context.Context, db *sqlx.DB, actor string) (int64, error) {
	var total int64
	var err error
	if actor != "" {
		err = db.GetContext(ctx, &total,
			`SELECT COALESCE(SUM(size_bytes), 0) FROM shared_artifacts WHERE created_by = $1`, actor)
	} else {
		err = db.GetContext(ctx, &total,
			`SELECT COALESCE(SUM(size_bytes), 0) FROM shared_artifacts`)
	}
	return total, err
}

func DeleteArtifact(ctx context.Context, db *sqlx.DB, id uuid.UUID) (string, error) {
	var artifactPath string
	err := db.GetContext(ctx, &artifactPath,
		`DELETE FROM shared_artifacts WHERE id = $1 RETURNING artifact_path`, id)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	return artifactPath, err
}
