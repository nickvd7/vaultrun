package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Status constants

const (
	SessionStatusCreated  = "created"
	SessionStatusRunning  = "running"
	SessionStatusStopped  = "stopped"
	SessionStatusError    = "error"

	RunStatusPending   = "pending"
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
	RunStatusTimeout   = "timeout"
)

// Audit action constants

const (
	ActionSessionCreated  = "session.created"
	ActionSessionDeleted  = "session.deleted"
	ActionFileUploaded    = "file.uploaded"
	ActionFileDownloaded  = "file.downloaded"
	ActionCommandStarted  = "command.started"
	ActionCommandFinished = "command.finished"
	ActionCommandFailed   = "command.failed"
	ActionAPIKeyCreated   = "apikey.created"
	ActionAPIKeyRevoked   = "apikey.revoked"
	ActionFileDeleted     = "file.deleted"
	ActionSessionExpired  = "session.expired" // emitted by background cleanup goroutine
)

// JSONB is a map that implements sql Scanner/Valuer for Postgres JSONB.
type JSONB map[string]interface{}

func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return "{}", nil
	}
	b, err := json.Marshal(j)
	return string(b), err
}

func (j *JSONB) Scan(src interface{}) error {
	switch v := src.(type) {
	case []byte:
		return json.Unmarshal(v, j)
	case string:
		return json.Unmarshal([]byte(v), j)
	case nil:
		*j = JSONB{}
		return nil
	}
	return fmt.Errorf("unsupported JSONB source type %T", src)
}

// StringArray handles Postgres text[] arrays.
// Delegates to pq.StringArray which serialises to/from the native Postgres
// array literal format {"a","b"} — NOT JSON ["a","b"]. The previous
// json.Marshal implementation produced JSON which caused
// "pq: malformed array literal" errors at INSERT time.
type StringArray []string

func (a StringArray) Value() (driver.Value, error) {
	return pq.StringArray(a).Value()
}

func (a *StringArray) Scan(src interface{}) error {
	tmp := pq.StringArray(*a)
	if err := tmp.Scan(src); err != nil {
		return err
	}
	*a = StringArray(tmp)
	return nil
}

type APIKey struct {
	ID         uuid.UUID  `db:"id"           json:"id"`
	Name       string     `db:"name"         json:"name"`
	KeyHash    string     `db:"key_hash"     json:"-"`
	Prefix     string     `db:"prefix"       json:"prefix"`
	CreatedAt  time.Time  `db:"created_at"   json:"created_at"`
	LastUsedAt *time.Time `db:"last_used_at" json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `db:"expires_at"   json:"expires_at,omitempty"`
	Active     bool       `db:"active"       json:"active"`
}

type Session struct {
	ID              uuid.UUID  `db:"id"               json:"id"`
	Name            *string    `db:"name"             json:"name,omitempty"`
	Image           string     `db:"image"            json:"image"`
	Status          string     `db:"status"           json:"status"`
	ContainerID     *string    `db:"container_id"     json:"container_id,omitempty"`
	NetworkEnabled  bool       `db:"network_enabled"  json:"network_enabled"`
	CPULimit        float64    `db:"cpu_limit"        json:"cpu_limit"`
	MemoryLimitMB   int        `db:"memory_limit_mb"  json:"memory_limit_mb"`
	TimeoutSeconds  int        `db:"timeout_seconds"  json:"timeout_seconds"`
	WorkspacePath   string     `db:"workspace_path"   json:"-"` // internal host path — never expose to callers
	Labels          JSONB      `db:"labels"           json:"labels"`
	CreatedBy       string     `db:"created_by"       json:"created_by"`
	CreatedAt       time.Time  `db:"created_at"       json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"       json:"updated_at"`
	StoppedAt       *time.Time `db:"stopped_at"       json:"stopped_at,omitempty"`
}

type Run struct {
	ID             uuid.UUID   `db:"id"              json:"id"`
	SessionID      uuid.UUID   `db:"session_id"      json:"session_id"`
	Command        string      `db:"command"         json:"command"`
	Args           StringArray `db:"args"            json:"args"`
	Env            JSONB       `db:"env"             json:"-"` // may contain secrets — never expose to callers
	WorkingDir     string      `db:"working_dir"     json:"working_dir"`
	Status         string      `db:"status"          json:"status"`
	ExitCode       *int        `db:"exit_code"       json:"exit_code,omitempty"`
	Stdout         *string     `db:"stdout"          json:"stdout,omitempty"`
	Stderr         *string     `db:"stderr"          json:"stderr,omitempty"`
	DurationMS     *int64      `db:"duration_ms"     json:"duration_ms,omitempty"`
	TimeoutSeconds int         `db:"timeout_seconds" json:"timeout_seconds"`
	CreatedAt      time.Time   `db:"created_at"      json:"created_at"`
	UpdatedAt      time.Time   `db:"updated_at"      json:"updated_at"`
	StartedAt       *time.Time  `db:"started_at"       json:"started_at,omitempty"`
	FinishedAt      *time.Time  `db:"finished_at"      json:"finished_at,omitempty"`
	// CallbackURL is stored but never returned in API responses (may contain secrets).
	CallbackURL     *string     `db:"callback_url"     json:"-"`
	// OutputTruncated is not persisted; set in-memory when docker capped output.
	OutputTruncated bool        `db:"-"                json:"output_truncated,omitempty"`
}

type File struct {
	ID          uuid.UUID `db:"id"           json:"id"`
	SessionID   uuid.UUID `db:"session_id"   json:"session_id"`
	Path        string    `db:"path"         json:"path"`
	SizeBytes   int64     `db:"size_bytes"   json:"size_bytes"`
	ContentType string    `db:"content_type" json:"content_type"`
	CreatedAt   time.Time `db:"created_at"   json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"   json:"updated_at"`
}

type AuditLog struct {
	ID        uuid.UUID  `db:"id"         json:"id"`
	Timestamp time.Time  `db:"timestamp"  json:"timestamp"`
	Actor     string     `db:"actor"      json:"actor"`
	SessionID *uuid.UUID `db:"session_id" json:"session_id,omitempty"`
	RunID     *uuid.UUID `db:"run_id"     json:"run_id,omitempty"`
	Action    string     `db:"action"     json:"action"`
	Metadata  JSONB      `db:"metadata"   json:"metadata"`
}
