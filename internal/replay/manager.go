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

// Manager manages session replay checkpoints
type Manager struct {
	db         *sqlx.DB
	signingKey []byte
}

// New creates a new replay Manager
func New(db *sqlx.DB, signingKey []byte) *Manager {
	return &Manager{
		db:         db,
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
	// TODO: This will call the snapshot manager to create actual snapshot
	// For now, we'll create a placeholder snapshot record
	snapshotID := uuid.New()
	sizeBytes := int64(0) // TODO: Get actual size from snapshot
	
	// Check size limit
	if sizeBytes > MaxCheckpointSizeBytes {
		return nil, ErrCheckpointTooLarge
	}
	
	// 4. Redact sensitive environment variables
	sanitizedEnv := m.redactEnvVars(opts.EnvVars)
	
	// 5. Truncate output previews
	stdoutPreview := truncate(opts.Stdout, 500)
	stderrPreview := truncate(opts.Stderr, 500)
	
	// 6. Create checkpoint record
	checkpoint := &Checkpoint{
		ID:                  uuid.New(),
		SessionID:           opts.SessionID,
		RunID:               opts.RunID,
		CheckpointNumber:    number,
		Name:                opts.Name,
		Description:         opts.Description,
		WorkspaceSnapshotID: snapshotID,
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
	
	// 7. Sign checkpoint
	checkpoint.Signature = m.signCheckpoint(checkpoint)
	
	// 8. Insert into database
	if err := m.insertCheckpoint(ctx, checkpoint); err != nil {
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
	// TODO: Call snapshot manager to restore snapshot
	// For now, this is a placeholder
	
	// 5. Restore environment variables (if any)
	// TODO: Update container environment
	
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
	_, err := m.db.ExecContext(ctx, `
		DELETE FROM replay_checkpoints
		WHERE id IN (
			SELECT id FROM replay_checkpoints
			WHERE session_id = $1
			ORDER BY checkpoint_number ASC
			LIMIT 1
		)
	`, sessionID)
	
	return err
}

// insertCheckpoint inserts a checkpoint into the database
func (m *Manager) insertCheckpoint(ctx context.Context, cp *Checkpoint) error {
	_, err := m.db.NamedExecContext(ctx, `
		INSERT INTO replay_checkpoints (
			id, session_id, run_id, checkpoint_number, name, description,
			workspace_snapshot_id, env_vars_snapshot, command, args,
			exit_code, duration_ms, stdout_preview, stderr_preview,
			signature, created_at, size_bytes
		) VALUES (
			:id, :session_id, :run_id, :checkpoint_number, :name, :description,
			:workspace_snapshot_id, :env_vars_snapshot, :command, :args,
			:exit_code, :duration_ms, :stdout_preview, :stderr_preview,
			:signature, :created_at, :size_bytes
		)
	`, cp)
	
	return err
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
