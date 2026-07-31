package replay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/models"
)

var (
	// ErrCheckpointNotFound is returned when a checkpoint doesn't exist
	ErrCheckpointNotFound = errors.New("checkpoint not found")
	
	// ErrCheckpointLimitExceeded is returned when session has too many checkpoints
	ErrCheckpointLimitExceeded = errors.New("checkpoint limit exceeded")
	
	// ErrCheckpointTooLarge is returned when workspace is too large for checkpoint
	ErrCheckpointTooLarge = errors.New("checkpoint size exceeds limit")
	
	// ErrOrgStorageLimitExceeded is returned when org has too much checkpoint storage
	ErrOrgStorageLimitExceeded = errors.New("org checkpoint storage limit exceeded")
	
	// ErrCheckpointTampered is returned when HMAC signature is invalid
	ErrCheckpointTampered = errors.New("checkpoint signature invalid - possible tampering")
	
	// ErrActiveCommandsRunning is returned when trying to restore with active commands
	ErrActiveCommandsRunning = errors.New("cannot restore: active commands running")
	
	// SensitivePathPatterns are excluded from checkpoints for security
	SensitivePathPatterns = []string{
		"**/*.key", "**/*.pem", "**/.ssh/*", "**/.aws/*",
		"**/id_rsa", "**/id_ed25519", "**/.env",
		"**/credentials", "**/secrets.yaml", "**/secret.*",
	}
	
	// SensitiveEnvVars are redacted from checkpoint env snapshots
	SensitiveEnvVars = []string{
		"AWS_SECRET_ACCESS_KEY", "DATABASE_PASSWORD",
		"API_KEY", "SECRET_KEY", "PRIVATE_KEY",
		"PASSWORD", "TOKEN", "SECRET",
	}
)

// WorkspaceManager defines the interface for workspace operations
type WorkspaceManager interface {
	CreateSnapshot(sessionID, snapshotID uuid.UUID) (archivePath string, sizeBytes int64, err error)
	RestoreSnapshot(sessionID uuid.UUID, archivePath string) error
	DeleteSnapshot(archivePath string) error
}

// Manager manages session replay checkpoints
type Manager struct {
	db         *sqlx.DB
	ws         WorkspaceManager
	signingKey []byte
}

// New creates a new replay Manager
func New(db *sqlx.DB, ws WorkspaceManager, signingKey []byte) *Manager {
	return &Manager{
		db:         db,
		ws:         ws,
		signingKey: signingKey,
	}
}

// CreateCheckpoint creates a new checkpoint for a session.
// This should be called after command execution with the run results.
func (m *Manager) CreateCheckpoint(ctx context.Context, opts CreateCheckpointOpts) (*Checkpoint, error) {
	// 1. Check session limits
	count, err := m.countCheckpoints(ctx, opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("count checkpoints: %w", err)
	}
	
	if count >= MaxCheckpointsPerSession {
		// Auto-prune oldest checkpoint
		if err := m.pruneOldestCheckpoint(ctx, opts.SessionID); err != nil {
			return nil, fmt.Errorf("prune oldest: %w", err)
		}
	}
	
	// 2. Get next checkpoint number (atomic)
	number, err := m.getNextCheckpointNumber(ctx, opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get checkpoint number: %w", err)
	}
	
	// 3. Create workspace snapshot
	snapshotID := uuid.New()
	archivePath, sizeBytes, err := m.ws.CreateSnapshot(opts.SessionID, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("create workspace snapshot: %w", err)
	}
	
	// Check size limit
	if sizeBytes > MaxCheckpointSizeBytes {
		// Clean up snapshot on size violation
		_ = m.ws.DeleteSnapshot(archivePath)
		return nil, ErrCheckpointTooLarge
	}
	
	// 4. Check org storage limit
	if err := m.checkOrgStorageLimit(ctx, opts.SessionID, sizeBytes); err != nil {
		// Clean up snapshot on storage limit violation
		_ = m.ws.DeleteSnapshot(archivePath)
		return nil, err
	}
	
	// 5. Redact sensitive environment variables
	sanitizedEnv := m.redactEnvVars(opts.EnvVars)
	
	// 6. Truncate output previews
	stdoutPreview := truncate(opts.Stdout, 500)
	stderrPreview := truncate(opts.Stderr, 500)
	
	// 7. Create checkpoint record
	checkpoint := &Checkpoint{
		ID:                  uuid.New(),
		SessionID:           opts.SessionID,
		RunID:               opts.RunID,
		CheckpointNumber:    number,
		Name:                opts.Name,
		Description:         opts.Description,
		WorkspaceSnapshotID: snapshotID,
		ArchivePath:         archivePath,
		EnvVarsSnapshot:     sanitizedEnv,
		Command:             opts.Command,
		Args:                argsToJSON(opts.Args),
		ExitCode:            opts.ExitCode,
		DurationMs:          opts.DurationMs,
		StdoutPreview:       stdoutPreview,
		StderrPreview:       stderrPreview,
		CreatedAt:           time.Now(),
		SizeBytes:           sizeBytes,
	}
	
	// 8. Sign checkpoint
	checkpoint.Signature = m.signCheckpoint(checkpoint)
	
	// 9. Insert into database
	if err := m.insertCheckpoint(ctx, checkpoint); err != nil {
		// Clean up snapshot on DB failure
		_ = m.ws.DeleteSnapshot(archivePath)
		return nil, fmt.Errorf("insert checkpoint: %w", err)
	}
	
	return checkpoint, nil
}

// RestoreCheckpoint restores a session to a previous checkpoint state
func (m *Manager) RestoreCheckpoint(ctx context.Context, opts RestoreOpts) error {
	// 1. Get checkpoint
	checkpoint, err := m.GetCheckpoint(ctx, opts.CheckpointID)
	if err != nil {
		return err
	}
	
	// Verify it belongs to the correct session
	if checkpoint.SessionID != opts.SessionID {
		return errors.New("checkpoint does not belong to this session")
	}
	
	// 2. Verify signature
	if !m.verifyCheckpoint(checkpoint) {
		return ErrCheckpointTampered
	}
	
	// 3. Check for active commands
	if !opts.StopActiveCommands {
		activeRuns, err := m.getActiveRuns(ctx, opts.SessionID)
		if err != nil {
			return fmt.Errorf("check active runs: %w", err)
		}
		if len(activeRuns) > 0 {
			return fmt.Errorf("%w: %d commands running", ErrActiveCommandsRunning, len(activeRuns))
		}
	}
	
	// 4. Restore workspace snapshot
	if err := m.ws.RestoreSnapshot(opts.SessionID, checkpoint.ArchivePath); err != nil {
		return fmt.Errorf("restore workspace: %w", err)
	}
	
	// 5. Restore environment variables (if any)
	// NOTE: Environment restoration requires container restart or exec
	// For now, env vars are restored at next command execution
	
	return nil
}

// ForkFromCheckpoint creates a new session from a checkpoint
func (m *Manager) ForkFromCheckpoint(ctx context.Context, opts ForkOpts) (*uuid.UUID, error) {
	// 1. Get checkpoint
	checkpoint, err := m.GetCheckpoint(ctx, opts.CheckpointID)
	if err != nil {
		return nil, err
	}
	
	// 2. Verify signature
	if !m.verifyCheckpoint(checkpoint) {
		return nil, ErrCheckpointTampered
	}
	
	// 3. Get original session config
	// TODO: Get session from database
	// For now, return placeholder
	newSessionID := uuid.New()
	
	// 4. Create new session with same config
	// TODO: Call session manager to create session
	
	// 5. Restore checkpoint to new session
	// TODO: Restore workspace snapshot to new session
	
	return &newSessionID, nil
}

// ListCheckpoints lists checkpoints for a session
func (m *Manager) ListCheckpoints(ctx context.Context, opts ListOpts) ([]*Checkpoint, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultCheckpointLimit
	}
	
	checkpoints := make([]*Checkpoint, 0)
	err := m.db.SelectContext(ctx, &checkpoints, `
		SELECT * FROM replay_checkpoints
		WHERE session_id = $1
		ORDER BY checkpoint_number DESC
		LIMIT $2 OFFSET $3
	`, opts.SessionID, limit, opts.Offset)
	
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	
	return checkpoints, nil
}

// GetCheckpoint retrieves a specific checkpoint
func (m *Manager) GetCheckpoint(ctx context.Context, id uuid.UUID) (*Checkpoint, error) {
	var cp Checkpoint
	err := m.db.GetContext(ctx, &cp, `
		SELECT * FROM replay_checkpoints WHERE id = $1
	`, id)
	
	if err == sql.ErrNoRows {
		return nil, ErrCheckpointNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}
	
	return &cp, nil
}

// DeleteCheckpoint deletes a checkpoint
func (m *Manager) DeleteCheckpoint(ctx context.Context, id uuid.UUID) error {
	// Get checkpoint to delete its snapshot file
	checkpoint, err := m.GetCheckpoint(ctx, id)
	if err != nil {
		return err
	}
	
	// Delete from database
	result, err := m.db.ExecContext(ctx, `
		DELETE FROM replay_checkpoints WHERE id = $1
	`, id)
	
	if err != nil {
		return fmt.Errorf("delete checkpoint: %w", err)
	}
	
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrCheckpointNotFound
	}
	
	// Best effort: delete snapshot file
	_ = m.ws.DeleteSnapshot(checkpoint.ArchivePath)
	
	return nil
}

// signCheckpoint creates an HMAC signature for a checkpoint
func (m *Manager) signCheckpoint(cp *Checkpoint) string {
	data := fmt.Sprintf("%s:%s:%d:%d",
		cp.SessionID.String(),
		cp.WorkspaceSnapshotID.String(),
		cp.CheckpointNumber,
		cp.CreatedAt.Unix(),
	)
	
	h := hmac.New(sha256.New, m.signingKey)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

// verifyCheckpoint verifies a checkpoint's HMAC signature
func (m *Manager) verifyCheckpoint(cp *Checkpoint) bool {
	expectedSig := m.signCheckpoint(cp)
	return hmac.Equal([]byte(cp.Signature), []byte(expectedSig))
}

// redactEnvVars redacts sensitive environment variables
func (m *Manager) redactEnvVars(env map[string]string) models.JSONB {
	if env == nil {
		return models.JSONB{}
	}
	
	result := make(models.JSONB)
	for k, v := range env {
		// Check if key contains sensitive patterns
		keyUpper := strings.ToUpper(k)
		isSensitive := false
		for _, pattern := range SensitiveEnvVars {
			if strings.Contains(keyUpper, pattern) {
				isSensitive = true
				break
			}
		}
		
		if isSensitive {
			result[k] = "[REDACTED]"
		} else {
			result[k] = v
		}
	}
	
	return result
}

// countCheckpoints counts checkpoints for a session
func (m *Manager) countCheckpoints(ctx context.Context, sessionID uuid.UUID) (int, error) {
	var count int
	err := m.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM replay_checkpoints WHERE session_id = $1
	`, sessionID)
	return count, err
}

// getNextCheckpointNumber gets the next checkpoint number atomically
func (m *Manager) getNextCheckpointNumber(ctx context.Context, sessionID uuid.UUID) (int, error) {
	tx, err := m.db.BeginTxx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	
	var maxNum sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT MAX(checkpoint_number) 
		FROM replay_checkpoints 
		WHERE session_id = $1
		FOR UPDATE
	`, sessionID).Scan(&maxNum)
	
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	
	nextNum := 0
	if maxNum.Valid {
		nextNum = int(maxNum.Int64) + 1
	}
	
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	
	return nextNum, nil
}

// pruneOldestCheckpoint deletes the oldest checkpoint for a session
func (m *Manager) pruneOldestCheckpoint(ctx context.Context, sessionID uuid.UUID) error {
	// Get the oldest checkpoint to delete its snapshot file
	var archivePath string
	err := m.db.GetContext(ctx, &archivePath, `
		SELECT archive_path FROM replay_checkpoints
		WHERE session_id = $1
		ORDER BY checkpoint_number ASC
		LIMIT 1
	`, sessionID)
	
	if err != nil {
		return err
	}
	
	// Delete from database first
	_, err = m.db.ExecContext(ctx, `
		DELETE FROM replay_checkpoints
		WHERE id IN (
			SELECT id FROM replay_checkpoints
			WHERE session_id = $1
			ORDER BY checkpoint_number ASC
			LIMIT 1
		)
	`, sessionID)
	
	if err != nil {
		return err
	}
	
	// Best effort: delete snapshot file
	_ = m.ws.DeleteSnapshot(archivePath)
	
	return nil
}

// insertCheckpoint inserts a checkpoint into the database
func (m *Manager) insertCheckpoint(ctx context.Context, cp *Checkpoint) error {
	_, err := m.db.NamedExecContext(ctx, `
		INSERT INTO replay_checkpoints (
			id, session_id, run_id, checkpoint_number, name, description,
			workspace_snapshot_id, archive_path, env_vars_snapshot, command, args,
			exit_code, duration_ms, stdout_preview, stderr_preview,
			signature, created_at, size_bytes
		) VALUES (
			:id, :session_id, :run_id, :checkpoint_number, :name, :description,
			:workspace_snapshot_id, :archive_path, :env_vars_snapshot, :command, :args,
			:exit_code, :duration_ms, :stdout_preview, :stderr_preview,
			:signature, :created_at, :size_bytes
		)
	`, cp)
	
	return err
}

// checkOrgStorageLimit checks if adding sizeBytes would exceed the org storage limit
func (m *Manager) checkOrgStorageLimit(ctx context.Context, sessionID uuid.UUID, sizeBytes int64) error {
	// Get session to find org
	var orgID *uuid.UUID
	err := m.db.GetContext(ctx, &orgID, `
		SELECT org_id FROM sessions WHERE id = $1
	`, sessionID)
	
	if err != nil || orgID == nil {
		// No org or session owner - skip org limit check
		return nil
	}
	
	// Calculate total checkpoint storage for org
	var totalStorage int64
	err = m.db.GetContext(ctx, &totalStorage, `
		SELECT COALESCE(SUM(rc.size_bytes), 0)
		FROM replay_checkpoints rc
		JOIN sessions s ON rc.session_id = s.id
		WHERE s.org_id = $1
	`, orgID)
	
	if err != nil {
		return fmt.Errorf("check org storage: %w", err)
	}
	
	if totalStorage+sizeBytes > MaxTotalCheckpointStoragePerOrg {
		return ErrOrgStorageLimitExceeded
	}
	
	return nil
}

// getActiveRuns returns active runs for a session
func (m *Manager) getActiveRuns(ctx context.Context, sessionID uuid.UUID) ([]uuid.UUID, error) {
	var runIDs []uuid.UUID
	err := m.db.SelectContext(ctx, &runIDs, `
		SELECT id FROM runs
		WHERE session_id = $1 AND status IN ('pending', 'running')
	`, sessionID)
	
	return runIDs, err
}

// Helper functions

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func argsToJSON(args []string) models.JSONB {
	if args == nil {
		return models.JSONB{}
	}
	result := make(models.JSONB)
	for i, arg := range args {
		result[fmt.Sprintf("%d", i)] = arg
	}
	return result
}
