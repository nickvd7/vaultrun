package replay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

	// ErrSessionNotFound is returned when the session a checkpoint refers to
	// does not exist
	ErrSessionNotFound = errors.New("session not found")

	// ErrReplayDisabled is returned when a session has not opted into replay
	ErrReplayDisabled = errors.New("replay is not enabled for this session")

	// ErrInvalidCheckpoint is returned when checkpoint input fails validation
	ErrInvalidCheckpoint = errors.New("invalid checkpoint")
	
	// SensitivePathPatterns are excluded from checkpoints for security
	SensitivePathPatterns = []string{
		"**/*.key", "**/*.pem", "**/.ssh/*", "**/.aws/*",
		"**/id_rsa", "**/id_ed25519", "**/.env",
		"**/credentials", "**/secrets.yaml", "**/secret.*",
	}
	
	// SensitiveEnvVars are substrings that mark an environment variable as
	// secret; a matching variable is replaced with [REDACTED] in checkpoint
	// snapshots. Matching is substring-based and case-insensitive, so short
	// generic fragments cover many concrete names: "KEY" covers
	// AWS_ACCESS_KEY_ID, SSH_KEY and API_KEY alike.
	//
	// Erring toward over-redaction is deliberate: a redacted non-secret is a
	// minor annoyance during debugging, a leaked credential is not.
	SensitiveEnvVars = []string{
		"PASSWORD", "PASSWD", "PWD",
		"SECRET", "TOKEN", "KEY",
		"CREDENTIAL", "AUTH",
		"SALT", "PASSPHRASE", "CIPHER",
		"SESSION", "COOKIE",
		"CERT", "SIGNATURE", "SIGNING",
		"PRIVATE", "APIKEY",
		"DSN", "CONNECTION_STRING",
	}
)

// Bounds on caller-supplied checkpoint metadata. Stdout and stderr are
// truncated rather than rejected because a caller cannot control how much a
// command printed, but the fields it does choose are bounded so one checkpoint
// cannot carry megabytes of metadata.
const (
	MaxCheckpointNameLength        = 200
	MaxCheckpointDescriptionLength = 2000
	MaxCheckpointCommandLength     = 4096
	MaxCheckpointArgs              = 1000
	MaxCheckpointEnvVars           = 500
)

// validate bounds the caller-supplied fields of a checkpoint request.
func (o CreateCheckpointOpts) validate() error {
	if o.SessionID == uuid.Nil {
		return fmt.Errorf("%w: session_id is required", ErrInvalidCheckpoint)
	}
	if o.Name != nil && len(*o.Name) > MaxCheckpointNameLength {
		return fmt.Errorf("%w: name is %d characters, maximum is %d",
			ErrInvalidCheckpoint, len(*o.Name), MaxCheckpointNameLength)
	}
	if len(o.Description) > MaxCheckpointDescriptionLength {
		return fmt.Errorf("%w: description is %d characters, maximum is %d",
			ErrInvalidCheckpoint, len(o.Description), MaxCheckpointDescriptionLength)
	}
	if len(o.Command) > MaxCheckpointCommandLength {
		return fmt.Errorf("%w: command is %d characters, maximum is %d",
			ErrInvalidCheckpoint, len(o.Command), MaxCheckpointCommandLength)
	}
	if len(o.Args) > MaxCheckpointArgs {
		return fmt.Errorf("%w: args has %d entries, maximum is %d",
			ErrInvalidCheckpoint, len(o.Args), MaxCheckpointArgs)
	}
	if len(o.EnvVars) > MaxCheckpointEnvVars {
		return fmt.Errorf("%w: env_vars has %d entries, maximum is %d",
			ErrInvalidCheckpoint, len(o.EnvVars), MaxCheckpointEnvVars)
	}
	return nil
}

// WorkspaceManager defines the interface for workspace operations
type WorkspaceManager interface {
	Create(sessionID uuid.UUID) (path string, err error)
	Delete(sessionID uuid.UUID) error
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
	if err := opts.validate(); err != nil {
		return nil, err
	}

	// 0. Refuse sessions that have not opted into replay. Checked here rather
	// than in each caller so neither the API handler nor the runner hook can
	// bypass it: a checkpoint captures the workspace and environment, so
	// recording one for a session that did not ask for it is a data-retention
	// problem, not just a wasted snapshot.
	var replayEnabled bool
	err := m.db.GetContext(ctx, &replayEnabled,
		`SELECT COALESCE(replay_enabled, false) FROM sessions WHERE id = $1`, opts.SessionID)
	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("check replay enabled: %w", err)
	}
	if !replayEnabled {
		return nil, ErrReplayDisabled
	}

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
	
	// 2. Create workspace snapshot
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
	
	// 3. Check org storage limit
	if err := m.checkOrgStorageLimit(ctx, opts.SessionID, sizeBytes); err != nil {
		// Clean up snapshot on storage limit violation
		_ = m.ws.DeleteSnapshot(archivePath)
		return nil, err
	}
	
	// 4. Redact sensitive environment variables
	sanitizedEnv := m.redactEnvVars(opts.EnvVars)
	
	// 5. Truncate output previews
	stdoutPreview := truncate(opts.Stdout, 500)
	stderrPreview := truncate(opts.Stderr, 500)
	
	// 6. Create checkpoint record. CheckpointNumber and Signature are assigned
	// by insertCheckpoint, which needs a transaction to allocate the number
	// atomically and must sign the record after the number is known.
	checkpoint := &Checkpoint{
		ID:                  uuid.New(),
		SessionID:           opts.SessionID,
		RunID:               opts.RunID,
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
		CreatedAt:           time.Now().UTC(),
		SizeBytes:           sizeBytes,
	}
	
	// 7. Assign the checkpoint number, sign and insert — all in one transaction
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

// ForkFromCheckpoint creates a new session whose workspace is the checkpoint's
// snapshot, leaving the original session untouched.
//
// The new session inherits the source session's image, network configuration and
// org, because a fork is meant to reproduce the recorded state — running it
// against a different image would not reproduce anything. CPU and memory may be
// raised or lowered by the caller; the caller is responsible for bounding those
// against the deployment's limits before calling.
func (m *Manager) ForkFromCheckpoint(ctx context.Context, opts ForkOpts) (*uuid.UUID, error) {
	checkpoint, err := m.GetCheckpoint(ctx, opts.CheckpointID)
	if err != nil {
		return nil, err
	}

	// A fork copies recorded state into a live session, so the recording has to
	// be trustworthy first.
	if !m.verifyCheckpoint(checkpoint) {
		return nil, ErrCheckpointTampered
	}

	var src models.Session
	err = m.db.GetContext(ctx, &src,
		`SELECT * FROM sessions WHERE id = $1`, checkpoint.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load source session: %w", err)
	}

	newSessionID := uuid.New()
	wsPath, err := m.ws.Create(newSessionID)
	if err != nil {
		return nil, fmt.Errorf("create fork workspace: %w", err)
	}

	if err := m.ws.RestoreSnapshot(newSessionID, checkpoint.ArchivePath); err != nil {
		_ = m.ws.Delete(newSessionID)
		return nil, fmt.Errorf("restore snapshot into fork: %w", err)
	}

	name := opts.Name
	if name == "" {
		name = fmt.Sprintf("fork of checkpoint %d", checkpoint.CheckpointNumber)
	}
	if len(name) > MaxCheckpointNameLength {
		_ = m.ws.Delete(newSessionID)
		return nil, fmt.Errorf("%w: name is %d characters, maximum is %d",
			ErrInvalidCheckpoint, len(name), MaxCheckpointNameLength)
	}

	cpuLimit := src.CPULimit
	if opts.CPULimit != nil {
		cpuLimit = *opts.CPULimit
	}
	memoryLimit := src.MemoryLimitMB
	if opts.MemoryLimitMB != nil {
		memoryLimit = *opts.MemoryLimitMB
	}

	now := time.Now().UTC()
	fork := &models.Session{
		ID:                     newSessionID,
		Name:                   &name,
		Image:                  src.Image,
		Status:                 models.SessionStatusCreated,
		NetworkEnabled:         src.NetworkEnabled,
		CPULimit:               cpuLimit,
		MemoryLimitMB:          memoryLimit,
		TimeoutSeconds:         src.TimeoutSeconds,
		WorkspacePath:          wsPath,
		Labels:                 models.JSONB{},
		AllowedHosts:           src.AllowedHosts,
		CreatedBy:              src.CreatedBy,
		OrgID:                  src.OrgID,
		ReplayEnabled:          src.ReplayEnabled,
		ForkedFromCheckpointID: &checkpoint.ID,
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	if err := m.insertForkedSession(ctx, fork); err != nil {
		_ = m.ws.Delete(newSessionID)
		return nil, fmt.Errorf("persist forked session: %w", err)
	}

	return &newSessionID, nil
}

// insertForkedSession writes the forked session row.
//
// The session insert lives here rather than reusing db.CreateSession because a
// fork also records forked_from_checkpoint_id, which the generic helper does
// not set.
func (m *Manager) insertForkedSession(ctx context.Context, s *models.Session) error {
	_, err := m.db.NamedExecContext(ctx, `
		INSERT INTO sessions (
			id, name, image, status, network_enabled, cpu_limit, memory_limit_mb,
			timeout_seconds, workspace_path, labels, allowed_hosts, created_by,
			org_id, replay_enabled, forked_from_checkpoint_id, created_at, updated_at
		) VALUES (
			:id, :name, :image, :status, :network_enabled, :cpu_limit, :memory_limit_mb,
			:timeout_seconds, :workspace_path, :labels, :allowed_hosts, :created_by,
			:org_id, :replay_enabled, :forked_from_checkpoint_id, :created_at, :updated_at
		)
	`, s)
	return err
}

// ListCheckpoints lists checkpoints for a session.
//
// Limit and offset are clamped here rather than trusted from the caller: in
// Postgres a negative LIMIT means "no limit", so passing limit=-1 would return
// every checkpoint and a negative OFFSET is an outright query error.
func (m *Manager) ListCheckpoints(ctx context.Context, opts ListOpts) ([]*Checkpoint, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultCheckpointLimit
	}
	if limit > MaxCheckpointListLimit {
		limit = MaxCheckpointListLimit
	}

	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	
	checkpoints := make([]*Checkpoint, 0)
	err := m.db.SelectContext(ctx, &checkpoints, `
		SELECT * FROM replay_checkpoints
		WHERE session_id = $1
		ORDER BY checkpoint_number DESC
		LIMIT $2 OFFSET $3
	`, opts.SessionID, limit, offset)
	
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

// signCheckpoint computes the HMAC-SHA256 over every immutable field of a
// checkpoint.
//
// The signature must cover the execution context, not just the identity fields:
// a signature over only the IDs would let anyone with database access rewrite
// the recorded command, exit code or captured output while the checkpoint still
// verified — which defeats the purpose of signing it.
//
// Fields are separated by NUL bytes so that moving a delimiter character
// between two adjacent fields changes the digest (boundary confusion), matching
// the scheme used by the audit logger.
func (m *Manager) signCheckpoint(cp *Checkpoint) string {
	h := hmac.New(sha256.New, m.signingKey)

	writeField := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}

	writeField(cp.SessionID.String())
	writeField(cp.WorkspaceSnapshotID.String())
	writeField(strconv.Itoa(cp.CheckpointNumber))
	writeField(strconv.FormatInt(cp.CreatedAt.Unix(), 10))
	writeField(cp.ArchivePath)
	writeField(cp.Command)
	writeField(cp.StdoutPreview)
	writeField(cp.StderrPreview)
	writeField(strconv.FormatInt(cp.SizeBytes, 10))

	// Optional fields contribute a stable placeholder when unset so that
	// "absent" and "empty" cannot be swapped.
	if cp.RunID != nil {
		writeField(cp.RunID.String())
	} else {
		writeField("")
	}
	if cp.ExitCode != nil {
		writeField(strconv.Itoa(*cp.ExitCode))
	} else {
		writeField("")
	}
	if cp.DurationMs != nil {
		writeField(strconv.Itoa(*cp.DurationMs))
	} else {
		writeField("")
	}

	// Args and the redacted env snapshot are JSON-encoded. Both are stored as
	// JSONB, so marshalling here matches what the database round-trips.
	argsJSON, _ := json.Marshal(cp.Args)
	writeField(string(argsJSON))
	envJSON, _ := json.Marshal(cp.EnvVarsSnapshot)
	writeField(string(envJSON))

	return hex.EncodeToString(h.Sum(nil))
}

// verifyCheckpoint reports whether a checkpoint's stored signature matches a
// freshly computed one. Returns false when signing is not configured, so a
// deployment without a key cannot silently accept unsigned checkpoints.
func (m *Manager) verifyCheckpoint(cp *Checkpoint) bool {
	if len(m.signingKey) == 0 || cp.Signature == "" {
		return false
	}

	expected := m.signCheckpoint(cp)

	// Compare the decoded bytes: hmac.Equal on hex strings of differing length
	// short-circuits, and comparing raw digests is the conventional form.
	got, err := hex.DecodeString(cp.Signature)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}

	return hmac.Equal(got, want)
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

// sessionLockKey derives a stable 64-bit advisory-lock key from a session ID.
// Advisory locks are keyed by integer, so the UUID's first eight bytes are
// reinterpreted; collisions between different sessions only cost concurrency,
// never correctness, because the unique constraint on
// (session_id, checkpoint_number) is the real guarantee.
func sessionLockKey(sessionID uuid.UUID) int64 {
	return int64(binary.BigEndian.Uint64(sessionID[:8]))
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
// insertCheckpoint allocates the next checkpoint number for the session, signs
// the record and writes it — all inside one transaction.
//
// The number cannot be allocated in a separate step: between reading MAX and
// inserting, a concurrent checkpoint on the same session would read the same
// value and one of the two inserts would violate the unique constraint on
// (session_id, checkpoint_number). A transaction-scoped advisory lock serialises
// the read-modify-write per session and is released on commit or rollback.
//
// A row lock is not usable here: Postgres rejects FOR UPDATE on a query with an
// aggregate, and there is no row to lock for the first checkpoint anyway.
func (m *Manager) insertCheckpoint(ctx context.Context, cp *Checkpoint) error {
	tx, err := m.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock($1)`, sessionLockKey(cp.SessionID)); err != nil {
		return fmt.Errorf("acquire checkpoint lock: %w", err)
	}

	var maxNum sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT MAX(checkpoint_number) FROM replay_checkpoints WHERE session_id = $1`,
		cp.SessionID).Scan(&maxNum)
	if err != nil {
		return fmt.Errorf("read highest checkpoint number: %w", err)
	}

	cp.CheckpointNumber = 1
	if maxNum.Valid {
		cp.CheckpointNumber = int(maxNum.Int64) + 1
	}

	// Sign only now: the checkpoint number is part of the signed payload.
	cp.Signature = m.signCheckpoint(cp)

	if _, err := tx.NamedExecContext(ctx, `
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
	`, cp); err != nil {
		return err
	}

	return tx.Commit()
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

// truncate shortens s to at most maxLen bytes without splitting a UTF-8
// character.
//
// Cutting at a byte offset can land in the middle of a multi-byte rune, and
// Postgres rejects the resulting invalid byte sequence for a text column — so a
// command that printed non-ASCII output used to fail checkpoint creation
// outright. Any trailing partial rune is dropped.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	cut := maxLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	// A rune boundary was found, but the rune starting there may itself be
	// truncated; validate and step back once more if so.
	out := s[:cut]
	for len(out) > 0 && !utf8.ValidString(out) {
		out = out[:len(out)-1]
	}
	return out
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
