package replay

import (
	"time"

	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/internal/models"
)

// Checkpoint represents a point-in-time snapshot of a session.
// Can be restored or forked to enable time-travel debugging.
type Checkpoint struct {
	ID                  uuid.UUID      `json:"id"`
	SessionID           uuid.UUID      `json:"session_id"`
	RunID               *uuid.UUID     `json:"run_id,omitempty"`
	CheckpointNumber    int            `json:"checkpoint_number"`
	Name                *string        `json:"name,omitempty"`
	Description         string         `json:"description"`
	WorkspaceSnapshotID uuid.UUID      `json:"workspace_snapshot_id"`
	EnvVarsSnapshot     models.JSONB   `json:"env_vars_snapshot,omitempty"`
	Command             string         `json:"command,omitempty"`
	Args                models.JSONB   `json:"args,omitempty"`
	ExitCode            *int           `json:"exit_code,omitempty"`
	DurationMs          *int           `json:"duration_ms,omitempty"`
	StdoutPreview       string         `json:"stdout_preview,omitempty"`
	StderrPreview       string         `json:"stderr_preview,omitempty"`
	Signature           string         `json:"signature"`
	CreatedAt           time.Time      `json:"created_at"`
	SizeBytes           int64          `json:"size_bytes"`
}

// CreateCheckpointOpts contains options for creating a checkpoint.
type CreateCheckpointOpts struct {
	SessionID   uuid.UUID
	RunID       *uuid.UUID
	Name        *string
	Description string
	Command     string
	Args        []string
	ExitCode    *int
	DurationMs  *int
	Stdout      string
	Stderr      string
	EnvVars     map[string]string
}

// RestoreOpts contains options for restoring a checkpoint.
type RestoreOpts struct {
	SessionID    uuid.UUID
	CheckpointID uuid.UUID
	StopActiveCommands bool  // If true, stop active commands before restoring
}

// ForkOpts contains options for forking a checkpoint.
type ForkOpts struct {
	CheckpointID uuid.UUID
	Name         string  // Name for new session
	CPULimit     *float64
	MemoryLimitMB *int
}

// ListOpts contains options for listing checkpoints.
type ListOpts struct {
	SessionID uuid.UUID
	Limit     int
	Offset    int
}

var (
	// DefaultCheckpointLimit is the default number of checkpoints to return
	DefaultCheckpointLimit = 50
	
	// MaxCheckpointsPerSession is the maximum checkpoints allowed per session
	MaxCheckpointsPerSession = 50
	
	// MaxCheckpointSizeBytes is the maximum size of a single checkpoint
	MaxCheckpointSizeBytes = int64(2 * 1024 * 1024 * 1024) // 2GB
	
	// MaxTotalCheckpointStoragePerOrg is the maximum total checkpoint storage per org
	MaxTotalCheckpointStoragePerOrg = int64(100 * 1024 * 1024 * 1024) // 100GB
)
