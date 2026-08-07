package missions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var (
	ErrMissionNotFound   = errors.New("mission not found")
	ErrMissionSlugExists = errors.New("mission slug already exists")
	ErrInvalidMission    = errors.New("invalid mission")
)

type Manager struct {
	db *sqlx.DB
}

func New(db *sqlx.DB) *Manager { return &Manager{db: db} }

type missionRow struct {
	ID          uuid.UUID      `db:"id"`
	Slug        string         `db:"slug"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	OrgID       *uuid.UUID     `db:"org_id"`
	CreatedBy   string         `db:"created_by"`
	Version     string         `db:"version"`
	Published   bool           `db:"published"`
	UseCount    int            `db:"use_count"`
	Steps       []byte         `db:"steps"`
	Tags        pq.StringArray `db:"tags"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

func (r missionRow) toMission() (*Mission, error) {
	m := &Mission{
		ID: r.ID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		OrgID: r.OrgID, CreatedBy: r.CreatedBy, Version: r.Version,
		Published: r.Published, UseCount: r.UseCount, Tags: []string(r.Tags),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if len(r.Steps) > 0 {
		if err := json.Unmarshal(r.Steps, &m.Steps); err != nil {
			return nil, fmt.Errorf("unmarshal steps: %w", err)
		}
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Steps == nil {
		m.Steps = []Step{}
	}
	return m, nil
}

func (m *Manager) Create(ctx context.Context, orgID *uuid.UUID, actor string, req CreateMissionRequest) (*Mission, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	req.Normalize()
	var exists bool
	if err := m.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM missions WHERE slug=$1)`, req.Slug); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrMissionSlugExists
	}
	version := req.Version
	if version == "" {
		version = "1.0.0"
	}
	stepsJSON, err := json.Marshal(req.Steps)
	if err != nil {
		return nil, err
	}
	id := uuid.New()
	now := time.Now().UTC()
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO missions (id, slug, name, description, org_id, created_by, version, published, steps, tags, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
		id, req.Slug, req.Name, req.Description, orgID, actor, version, req.Published, stepsJSON, pq.Array(req.Tags), now)
	if err != nil {
		return nil, err
	}
	return m.Get(ctx, id)
}

func (m *Manager) Get(ctx context.Context, id uuid.UUID) (*Mission, error) {
	var row missionRow
	err := m.db.GetContext(ctx, &row, `SELECT * FROM missions WHERE id=$1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMissionNotFound
	}
	if err != nil {
		return nil, err
	}
	return row.toMission()
}

func (m *Manager) GetBySlug(ctx context.Context, slug string) (*Mission, error) {
	var row missionRow
	err := m.db.GetContext(ctx, &row, `SELECT * FROM missions WHERE slug=$1`, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMissionNotFound
	}
	if err != nil {
		return nil, err
	}
	return row.toMission()
}

func (m *Manager) List(ctx context.Context, filter MissionFilter) ([]Mission, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where := []string{"1=1"}
	args := []any{}
	if filter.Published != nil {
		args = append(args, *filter.Published)
		where = append(where, fmt.Sprintf("published=$%d", len(args)))
	}
	if s := strings.TrimSpace(filter.Search); s != "" {
		args = append(args, "%"+s+"%")
		where = append(where, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d OR slug ILIKE $%d)", len(args), len(args), len(args)))
	}
	args = append(args, limit)
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, offset)
	q := fmt.Sprintf(`SELECT * FROM missions WHERE %s ORDER BY updated_at DESC LIMIT $%d OFFSET $%d`,
		strings.Join(where, " AND "), len(args)-1, len(args))
	var rows []missionRow
	if err := m.db.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	out := make([]Mission, 0, len(rows))
	for _, r := range rows {
		miss, err := r.toMission()
		if err != nil {
			return nil, err
		}
		out = append(out, *miss)
	}
	return out, nil
}

func (m *Manager) Update(ctx context.Context, id uuid.UUID, req UpdateMissionRequest) (*Mission, error) {
	cur, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || utf8.RuneCountInString(name) > maxNameLen {
			return nil, fmt.Errorf("%w: invalid name", ErrInvalidMission)
		}
		cur.Name = name
	}
	if req.Description != nil {
		if utf8.RuneCountInString(*req.Description) > maxDescriptionLen {
			return nil, fmt.Errorf("%w: description too long", ErrInvalidMission)
		}
		cur.Description = *req.Description
	}
	if req.Version != nil {
		if utf8.RuneCountInString(*req.Version) > maxVersionLen {
			return nil, fmt.Errorf("%w: version too long", ErrInvalidMission)
		}
		cur.Version = *req.Version
	}
	if req.Published != nil {
		cur.Published = *req.Published
	}
	if req.Tags != nil {
		if err := validateTags(req.Tags); err != nil {
			return nil, err
		}
		cur.Tags = req.Tags
	}
	if req.Steps != nil {
		if err := validateSteps(req.Steps); err != nil {
			return nil, err
		}
		cur.Steps = req.Steps
	}
	stepsJSON, err := json.Marshal(cur.Steps)
	if err != nil {
		return nil, err
	}
	_, err = m.db.ExecContext(ctx, `
		UPDATE missions SET name=$2, description=$3, version=$4, published=$5, steps=$6, tags=$7, updated_at=NOW()
		WHERE id=$1`, id, cur.Name, cur.Description, cur.Version, cur.Published, stepsJSON, pq.Array(cur.Tags))
	if err != nil {
		return nil, err
	}
	return m.Get(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := m.db.ExecContext(ctx, `DELETE FROM missions WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMissionNotFound
	}
	return nil
}

// RecordRun inserts a mission_runs row (increments use_count via trigger).
func (m *Manager) RecordRun(ctx context.Context, missionID uuid.UUID, sessionID, orgID *uuid.UUID, status string, stepResults []any, runErr string) (*MissionRun, error) {
	if _, err := m.Get(ctx, missionID); err != nil {
		return nil, err
	}
	if status == "" {
		status = "recorded"
	}
	resultsJSON, err := json.Marshal(stepResults)
	if err != nil {
		return nil, err
	}
	id := uuid.New()
	now := time.Now().UTC()
	var finished *time.Time
	if status == "completed" || status == "failed" || status == "recorded" {
		finished = &now
	}
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO mission_runs (id, mission_id, session_id, org_id, status, step_results, error, created_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, missionID, sessionID, orgID, status, resultsJSON, runErr, now, finished)
	if err != nil {
		return nil, err
	}
	return &MissionRun{
		ID: id, MissionID: missionID, SessionID: sessionID, OrgID: orgID,
		Status: status, StepResults: stepResults, Error: runErr, CreatedAt: now, FinishedAt: finished,
	}, nil
}

func (m *Manager) ListRuns(ctx context.Context, missionID uuid.UUID, limit int) ([]MissionRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	type row struct {
		ID          uuid.UUID      `db:"id"`
		MissionID   uuid.UUID      `db:"mission_id"`
		SessionID   *uuid.UUID     `db:"session_id"`
		OrgID       *uuid.UUID     `db:"org_id"`
		Status      string         `db:"status"`
		StepResults []byte         `db:"step_results"`
		Error       sql.NullString `db:"error"`
		CreatedAt   time.Time      `db:"created_at"`
		FinishedAt  *time.Time     `db:"finished_at"`
	}
	var rows []row
	if err := m.db.SelectContext(ctx, &rows, `
		SELECT id, mission_id, session_id, org_id, status, step_results, error, created_at, finished_at
		FROM mission_runs WHERE mission_id=$1 ORDER BY created_at DESC LIMIT $2`, missionID, limit); err != nil {
		return nil, err
	}
	out := make([]MissionRun, 0, len(rows))
	for _, r := range rows {
		mr := MissionRun{
			ID: r.ID, MissionID: r.MissionID, SessionID: r.SessionID, OrgID: r.OrgID,
			Status: r.Status, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
		}
		if r.Error.Valid {
			mr.Error = r.Error.String
		}
		_ = json.Unmarshal(r.StepResults, &mr.StepResults)
		if mr.StepResults == nil {
			mr.StepResults = []any{}
		}
		out = append(out, mr)
	}
	return out, nil
}
