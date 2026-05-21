package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/policy"
)

const maxOutputBytes = 10 * 1024 * 1024 // 10 MB

// Runner coordinates command execution inside sandbox containers.
type Runner struct {
	db     *sqlx.DB
	docker *dockerpkg.Client
	audit  *audit.Logger
	hook   policy.Hook
}

func New(db *sqlx.DB, docker *dockerpkg.Client, al *audit.Logger, hook policy.Hook) *Runner {
	if hook == nil {
		hook = policy.AllowAll{}
	}
	return &Runner{db: db, docker: docker, audit: al, hook: hook}
}

type RunRequest struct {
	SessionID      uuid.UUID
	ContainerID    string
	Command        string
	Args           []string
	Env            map[string]string
	WorkingDir     string
	TimeoutSeconds int
	Actor          string
}

// Execute runs a command inside the sandbox container, persists the run record,
// and emits audit events. Output is buffered and returned in the Run model.
func (r *Runner) Execute(ctx context.Context, req RunRequest) (*models.Run, error) {
	run, err := r.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}

	startedAt := time.Now().UTC()
	result, execErr := r.docker.Exec(ctx, dockerpkg.ExecConfig{
		ContainerID:    req.ContainerID,
		Command:        req.Command,
		Args:           req.Args,
		Env:            req.Env,
		WorkingDir:     run.WorkingDir,
		TimeoutSeconds: run.TimeoutSeconds,
		MaxOutputBytes: maxOutputBytes,
	})
	finishedAt := time.Now().UTC()

	status := resolveStatus(execErr, result)
	durationMS := result.DurationMS

	params := dbpkg.UpdateRunParams{
		ID:         run.ID,
		Status:     status,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
		DurationMS: &durationMS,
	}
	if execErr == nil {
		params.ExitCode = &result.ExitCode
		params.Stdout = &result.Stdout
		params.Stderr = &result.Stderr
	}

	if err := dbpkg.UpdateRun(ctx, r.db, params); err != nil {
		slog.Error("update run record", "run_id", run.ID, "err", err)
	}

	r.emitFinish(ctx, req, run, status, durationMS)

	updated, err := dbpkg.GetRun(ctx, r.db, run.ID)
	if err != nil {
		return run, nil
	}
	// Surface output truncation on the response (not persisted to DB).
	if execErr == nil && result != nil && result.Truncated {
		updated.OutputTruncated = true
	}
	return updated, nil
}

// Stream runs a command and writes stdout/stderr chunks to the provided writers
// as they arrive. Useful for SSE streaming to the client.
func (r *Runner) Stream(ctx context.Context, req RunRequest, stdout, stderr io.Writer) (*models.Run, error) {
	run, err := r.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}

	startedAt := time.Now().UTC()
	result, execErr := r.docker.ExecStream(ctx, dockerpkg.ExecConfig{
		ContainerID:    req.ContainerID,
		Command:        req.Command,
		Args:           req.Args,
		Env:            req.Env,
		WorkingDir:     run.WorkingDir,
		TimeoutSeconds: run.TimeoutSeconds,
		MaxOutputBytes: maxOutputBytes,
	}, stdout, stderr)
	finishedAt := time.Now().UTC()

	status := resolveStatus(execErr, result)
	durationMS := result.DurationMS

	params := dbpkg.UpdateRunParams{
		ID:         run.ID,
		Status:     status,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
		DurationMS: &durationMS,
	}
	if execErr == nil {
		params.ExitCode = &result.ExitCode
	}

	if err := dbpkg.UpdateRun(ctx, r.db, params); err != nil {
		slog.Error("update run record", "run_id", run.ID, "err", err)
	}

	r.emitFinish(ctx, req, run, status, durationMS)

	updated, err := dbpkg.GetRun(ctx, r.db, run.ID)
	if err != nil {
		return run, nil
	}
	if execErr == nil && result != nil && result.Truncated {
		updated.OutputTruncated = true
	}
	return updated, nil
}

// prepareRun validates the request, enforces policy, creates the DB record,
// and emits the start audit event. Shared between Execute and Stream.
func (r *Runner) prepareRun(ctx context.Context, req RunRequest) (*models.Run, error) {
	if req.Command == "" {
		return nil, fmt.Errorf("command is required")
	}
	if strings.ContainsAny(req.Command, ";|&$`\\<>{}()") {
		return nil, fmt.Errorf("command contains disallowed characters")
	}

	// Validate env var keys: reject null bytes, newlines, and '=' which could
	// corrupt the env string passed to the Docker exec API (H-2).
	for k := range req.Env {
		if strings.ContainsAny(k, "=\x00\n\r") {
			return nil, fmt.Errorf("env key %q contains disallowed characters", k)
		}
	}
	// Validate env var values: reject null bytes.
	for k, v := range req.Env {
		if strings.ContainsRune(v, '\x00') {
			return nil, fmt.Errorf("env value for key %q contains disallowed characters", k)
		}
	}

	if d := r.hook.EvalCommand(ctx, req.SessionID, req.Command, req.Args); !d.Allowed {
		return nil, fmt.Errorf("command denied by policy: %s", d.Reason)
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 || timeout > 3600 {
		timeout = 30
	}

	now := time.Now().UTC()
	envMap := models.JSONB{}
	for k, v := range req.Env {
		envMap[k] = v
	}

	workingDir := req.WorkingDir
	if workingDir == "" {
		workingDir = "/workspace"
	}

	run := &models.Run{
		ID:             uuid.New(),
		SessionID:      req.SessionID,
		Command:        req.Command,
		Args:           models.StringArray(req.Args),
		Env:            envMap,
		WorkingDir:     workingDir,
		Status:         models.RunStatusPending,
		TimeoutSeconds: timeout,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := dbpkg.CreateRun(ctx, r.db, run); err != nil {
		// Log full error server-side; return a generic message so internal
		// DB details (table names, constraints) don't reach the caller.
		slog.Error("runner: persist run record", "err", err)
		return nil, fmt.Errorf("internal error: could not create run record")
	}

	sidCopy := req.SessionID
	r.audit.Log(ctx, audit.Event{
		Actor:     req.Actor,
		SessionID: &sidCopy,
		RunID:     &run.ID,
		Action:    models.ActionCommandStarted,
		Metadata:  models.JSONB{"command": req.Command, "args": req.Args},
	})

	return run, nil
}

func (r *Runner) emitFinish(ctx context.Context, req RunRequest, run *models.Run, status string, durationMS int64) {
	sidCopy := req.SessionID
	action := models.ActionCommandFinished
	if status == models.RunStatusFailed || status == models.RunStatusTimeout {
		action = models.ActionCommandFailed
	}
	r.audit.Log(ctx, audit.Event{
		Actor:     req.Actor,
		SessionID: &sidCopy,
		RunID:     &run.ID,
		Action:    action,
		Metadata:  models.JSONB{"status": status, "duration_ms": durationMS},
	})
}

func resolveStatus(execErr error, result *dockerpkg.ExecResult) string {
	if execErr != nil {
		slog.Error("exec error", "err", execErr)
		return models.RunStatusFailed
	}
	if result.TimedOut {
		return models.RunStatusTimeout
	}
	if result.ExitCode != 0 {
		return models.RunStatusFailed
	}
	return models.RunStatusCompleted
}
