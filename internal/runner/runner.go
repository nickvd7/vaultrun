package runner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/models"
)

const maxOutputBytes = 10 * 1024 * 1024 // 10 MB

// Runner coordinates command execution inside sandbox containers.
type Runner struct {
	db     *sqlx.DB
	docker *dockerpkg.Client
	audit  *audit.Logger
}

func New(db *sqlx.DB, docker *dockerpkg.Client, audit *audit.Logger) *Runner {
	return &Runner{db: db, docker: docker, audit: audit}
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
// and emits audit events.
func (r *Runner) Execute(ctx context.Context, req RunRequest) (*models.Run, error) {
	if req.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// Validate command — no shell metacharacters at the binary level.
	if strings.ContainsAny(req.Command, ";|&$`\\<>{}()") {
		return nil, fmt.Errorf("command contains disallowed characters")
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 || timeout > 3600 {
		timeout = 30
	}

	now := time.Now().UTC()
	run := &models.Run{
		ID:             uuid.New(),
		SessionID:      req.SessionID,
		Command:        req.Command,
		Args:           models.StringArray(req.Args),
		WorkingDir:     req.WorkingDir,
		Status:         models.RunStatusPending,
		TimeoutSeconds: timeout,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if run.WorkingDir == "" {
		run.WorkingDir = "/workspace"
	}

	envMap := models.JSONB{}
	for k, v := range req.Env {
		envMap[k] = v
	}
	run.Env = envMap

	if err := dbpkg.CreateRun(ctx, r.db, run); err != nil {
		return nil, fmt.Errorf("persist run: %w", err)
	}

	sidCopy := req.SessionID
	r.audit.Log(ctx, audit.Event{
		Actor:     req.Actor,
		SessionID: &sidCopy,
		RunID:     &run.ID,
		Action:    models.ActionCommandStarted,
		Metadata: models.JSONB{
			"command": req.Command,
			"args":    req.Args,
		},
	})

	startedAt := time.Now().UTC()
	result, execErr := r.docker.Exec(ctx, dockerpkg.ExecConfig{
		ContainerID:    req.ContainerID,
		Command:        req.Command,
		Args:           req.Args,
		Env:            req.Env,
		WorkingDir:     req.WorkingDir,
		TimeoutSeconds: timeout,
		MaxOutputBytes: maxOutputBytes,
	})

	finishedAt := time.Now().UTC()

	// Determine final status
	status := models.RunStatusCompleted
	if execErr != nil {
		status = models.RunStatusFailed
		slog.Error("exec error", "run_id", run.ID, "err", execErr)
	} else if result.TimedOut {
		status = models.RunStatusTimeout
	} else if result.ExitCode != 0 {
		status = models.RunStatusFailed
	}

	durationMS := result.DurationMS
	updateParams := dbpkg.UpdateRunParams{
		ID:         run.ID,
		Status:     status,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
		DurationMS: &durationMS,
	}

	if execErr == nil {
		updateParams.ExitCode = &result.ExitCode
		updateParams.Stdout = &result.Stdout
		updateParams.Stderr = &result.Stderr
	}

	if err := dbpkg.UpdateRun(ctx, r.db, updateParams); err != nil {
		slog.Error("update run record", "run_id", run.ID, "err", err)
	}

	auditAction := models.ActionCommandFinished
	if status == models.RunStatusFailed || status == models.RunStatusTimeout {
		auditAction = models.ActionCommandFailed
	}

	r.audit.Log(ctx, audit.Event{
		Actor:     req.Actor,
		SessionID: &sidCopy,
		RunID:     &run.ID,
		Action:    auditAction,
		Metadata: models.JSONB{
			"status":      status,
			"duration_ms": durationMS,
		},
	})

	// Reload updated run
	updated, err := dbpkg.GetRun(ctx, r.db, run.ID)
	if err != nil {
		return run, nil
	}
	return updated, nil
}
