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
		    memory_limit_mb, timeout_seconds, workspace_path, labels, created_by, created_at, updated_at)
		VALUES (:id, :name, :image, :status, :network_enabled, :cpu_limit,
		    :memory_limit_mb, :timeout_seconds, :workspace_path, :labels, :created_by, :created_at, :updated_at)
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
		INSERT INTO audit_logs (id, timestamp, actor, session_id, run_id, action, metadata)
		VALUES (:id, :timestamp, :actor, :session_id, :run_id, :action, :metadata)
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
	res, err := db.ExecContext(ctx, `UPDATE api_keys SET active = FALSE WHERE id = $1`, id)
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
