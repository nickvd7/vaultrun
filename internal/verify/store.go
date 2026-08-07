package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Record is a persisted verification outcome.
type Record struct {
	ID           uuid.UUID       `db:"id" json:"id"`
	SessionID    *uuid.UUID      `db:"session_id" json:"session_id,omitempty"`
	RunID        *uuid.UUID      `db:"run_id" json:"run_id,omitempty"`
	MissionRunID *uuid.UUID      `db:"mission_run_id" json:"mission_run_id,omitempty"`
	StepName     string          `db:"step_name" json:"step_name,omitempty"`
	Spec         json.RawMessage `db:"spec" json:"spec"`
	Observation  json.RawMessage `db:"observation" json:"observation"`
	Passed       bool            `db:"passed" json:"passed"`
	Checks       json.RawMessage `db:"checks" json:"checks"`
	CreatedAt    time.Time       `db:"created_at" json:"created_at"`
}

// Store persists verification results.
type Store struct {
	db *sqlx.DB
}

// NewStore creates a Store.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// Save inserts a verification record.
func (s *Store) Save(ctx context.Context, rec *Record) error {
	if rec.ID == uuid.Nil {
		rec.ID = uuid.New()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.Spec == nil {
		rec.Spec = json.RawMessage(`{}`)
	}
	if rec.Observation == nil {
		rec.Observation = json.RawMessage(`{}`)
	}
	if rec.Checks == nil {
		rec.Checks = json.RawMessage(`[]`)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_verifications (
			id, session_id, run_id, mission_run_id, step_name,
			spec, observation, passed, checks, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		rec.ID, rec.SessionID, rec.RunID, rec.MissionRunID, rec.StepName,
		rec.Spec, rec.Observation, rec.Passed, rec.Checks, rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert run_verification: %w", err)
	}
	return nil
}

// ListBySession returns recent verifications for a session.
func (s *Store) ListBySession(ctx context.Context, sessionID uuid.UUID, limit int) ([]Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []Record
	err := s.db.SelectContext(ctx, &rows, `
		SELECT id, session_id, run_id, mission_run_id, step_name,
		       spec, observation, passed, checks, created_at
		FROM run_verifications
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list run_verifications: %w", err)
	}
	if rows == nil {
		rows = []Record{}
	}
	return rows, nil
}
