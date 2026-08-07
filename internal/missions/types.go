package missions

import (
	"time"

	"github.com/google/uuid"
)

// Step is one tool invocation in a mission (MCP tool name + args).
type Step struct {
	Name        string            `json:"name"`
	Tool        string            `json:"tool"`
	Args        map[string]string `json:"args,omitempty"`
	Description string            `json:"description,omitempty"`
	// Verify is an optional post-step check evaluated by the verify package / API.
	Verify *StepVerify `json:"verify,omitempty"`
}

// StepVerify is a lightweight assertion attached to a step (evaluated when verify is enabled).
type StepVerify struct {
	ExitCodeZero   *bool  `json:"exit_code_zero,omitempty"`
	StdoutContains string `json:"stdout_contains,omitempty"`
	FileExists     string `json:"file_exists,omitempty"`
}

// Mission is a reusable, versioned tool sequence.
type Mission struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	Slug        string     `db:"slug" json:"slug"`
	Name        string     `db:"name" json:"name"`
	Description string     `db:"description" json:"description"`
	OrgID       *uuid.UUID `db:"org_id" json:"org_id,omitempty"`
	CreatedBy   string     `db:"created_by" json:"created_by"`
	Version     string     `db:"version" json:"version"`
	Published   bool       `db:"published" json:"published"`
	UseCount    int        `db:"use_count" json:"use_count"`
	Steps       []Step     `db:"steps" json:"steps"`
	Tags        []string   `db:"tags" json:"tags"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at" json:"updated_at"`
}

type CreateMissionRequest struct {
	Slug        string   `json:"slug" binding:"required"`
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Steps       []Step   `json:"steps" binding:"required"`
	Tags        []string `json:"tags"`
	Published   bool     `json:"published"`
}

type UpdateMissionRequest struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Version     *string  `json:"version"`
	Steps       []Step   `json:"steps"`
	Tags        []string `json:"tags"`
	Published   *bool    `json:"published"`
}

type MissionFilter struct {
	Search    string `form:"search"`
	Published *bool  `form:"published"`
	Limit     int    `form:"limit"`
	Offset    int    `form:"offset"`
}

// MissionRun records one execution attempt of a mission.
type MissionRun struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	MissionID   uuid.UUID  `db:"mission_id" json:"mission_id"`
	SessionID   *uuid.UUID `db:"session_id" json:"session_id,omitempty"`
	OrgID       *uuid.UUID `db:"org_id" json:"org_id,omitempty"`
	Status      string     `db:"status" json:"status"`
	StepResults []any      `db:"step_results" json:"step_results"`
	Error       string     `db:"error,omitempty" json:"error,omitempty"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
	FinishedAt  *time.Time `db:"finished_at" json:"finished_at,omitempty"`
}

type StartMissionRunRequest struct {
	SessionID *uuid.UUID `json:"session_id"`
}
