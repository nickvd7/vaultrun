package missions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CostAttribution is a snapshot of session costs linked to a mission run.
type CostAttribution struct {
	ID           uuid.UUID `db:"id" json:"id"`
	MissionID    uuid.UUID `db:"mission_id" json:"mission_id"`
	MissionRunID uuid.UUID `db:"mission_run_id" json:"mission_run_id"`
	SessionID    uuid.UUID `db:"session_id" json:"session_id"`
	ComputeCost  float64   `db:"compute_cost" json:"compute_cost"`
	StorageCost  float64   `db:"storage_cost" json:"storage_cost"`
	NetworkCost  float64   `db:"network_cost" json:"network_cost"`
	TotalCost    float64   `db:"total_cost" json:"total_cost"`
	MetricCount  int       `db:"metric_count" json:"metric_count"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
}

// MissionCostSummary aggregates attributed costs for a mission.
type MissionCostSummary struct {
	MissionID   uuid.UUID `db:"mission_id" json:"mission_id"`
	RunCount    int       `db:"run_count" json:"run_count"`
	ComputeCost float64   `db:"compute_cost" json:"compute_cost"`
	StorageCost float64   `db:"storage_cost" json:"storage_cost"`
	NetworkCost float64   `db:"network_cost" json:"network_cost"`
	TotalCost   float64   `db:"total_cost" json:"total_cost"`
}

// UpdateRunRequest finishes or updates a mission run (replay completion).
type UpdateRunRequest struct {
	Status      string `json:"status"`
	StepResults []any  `json:"step_results"`
	Error       string `json:"error"`
	Attribute   bool   `json:"attribute_costs"` // snapshot session costs when session_id set
}

var (
	ErrRunNotFound = errors.New("mission run not found")
)

// GetRun loads one mission run.
func (m *Manager) GetRun(ctx context.Context, missionID, runID uuid.UUID) (*MissionRun, error) {
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
	var r row
	err := m.db.GetContext(ctx, &r, `
		SELECT id, mission_id, session_id, org_id, status, step_results, error, created_at, finished_at
		FROM mission_runs WHERE id=$1 AND mission_id=$2`, runID, missionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	mr := &MissionRun{
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
	return mr, nil
}

// UpdateRun updates status/step_results and optionally finishes the run.
func (m *Manager) UpdateRun(ctx context.Context, missionID, runID uuid.UUID, req UpdateRunRequest) (*MissionRun, error) {
	cur, err := m.GetRun(ctx, missionID, runID)
	if err != nil {
		return nil, err
	}
	status := cur.Status
	if req.Status != "" {
		switch req.Status {
		case "pending", "recorded", "running", "completed", "failed", "cancelled":
			status = req.Status
		default:
			return nil, fmt.Errorf("%w: invalid status", ErrInvalidMission)
		}
	}
	results := cur.StepResults
	if req.StepResults != nil {
		raw, err := json.Marshal(req.StepResults)
		if err != nil {
			return nil, err
		}
		if len(raw) > 256*1024 {
			return nil, fmt.Errorf("%w: step_results too large", ErrInvalidMission)
		}
		results = req.StepResults
	}
	runErr := cur.Error
	if req.Error != "" || req.Status == "failed" {
		runErr = req.Error
	}
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var finished *time.Time = cur.FinishedAt
	switch status {
	case "completed", "failed", "recorded", "cancelled":
		if finished == nil {
			finished = &now
		}
	case "pending", "running":
		finished = nil
	}
	_, err = m.db.ExecContext(ctx, `
		UPDATE mission_runs SET status=$1, step_results=$2, error=$3, finished_at=$4
		WHERE id=$5 AND mission_id=$6`,
		status, resultsJSON, runErr, finished, runID, missionID)
	if err != nil {
		return nil, fmt.Errorf("update mission run: %w", err)
	}
	return m.GetRun(ctx, missionID, runID)
}

// AttributeRunCosts snapshots current session cost_metrics totals onto the mission run.
func (m *Manager) AttributeRunCosts(ctx context.Context, missionID, runID uuid.UUID) (*CostAttribution, error) {
	run, err := m.GetRun(ctx, missionID, runID)
	if err != nil {
		return nil, err
	}
	if run.SessionID == nil {
		return nil, fmt.Errorf("%w: mission run has no session_id", ErrInvalidMission)
	}
	sessionID := *run.SessionID

	var totals struct {
		ComputeCost float64 `db:"compute_cost"`
		StorageCost float64 `db:"storage_cost"`
		NetworkCost float64 `db:"network_cost"`
		TotalCost   float64 `db:"total_cost"`
		MetricCount int     `db:"metric_count"`
	}
	err = m.db.GetContext(ctx, &totals, `
		SELECT
			COALESCE(SUM(compute_cost), 0) AS compute_cost,
			COALESCE(SUM(storage_cost), 0) AS storage_cost,
			COALESCE(SUM(network_cost), 0) AS network_cost,
			COALESCE(SUM(total_cost), 0) AS total_cost,
			COUNT(*)::int AS metric_count
		FROM cost_metrics WHERE session_id = $1`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sum session costs: %w", err)
	}

	attr := &CostAttribution{
		ID:           uuid.New(),
		MissionID:    missionID,
		MissionRunID: runID,
		SessionID:    sessionID,
		ComputeCost:  totals.ComputeCost,
		StorageCost:  totals.StorageCost,
		NetworkCost:  totals.NetworkCost,
		TotalCost:    totals.TotalCost,
		MetricCount:  totals.MetricCount,
		CreatedAt:    time.Now().UTC(),
	}
	_, err = m.db.ExecContext(ctx, `
		INSERT INTO mission_cost_attributions (
			id, mission_id, mission_run_id, session_id,
			compute_cost, storage_cost, network_cost, total_cost, metric_count, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (mission_run_id) DO UPDATE SET
			compute_cost = EXCLUDED.compute_cost,
			storage_cost = EXCLUDED.storage_cost,
			network_cost = EXCLUDED.network_cost,
			total_cost = EXCLUDED.total_cost,
			metric_count = EXCLUDED.metric_count,
			created_at = EXCLUDED.created_at
		`,
		attr.ID, attr.MissionID, attr.MissionRunID, attr.SessionID,
		attr.ComputeCost, attr.StorageCost, attr.NetworkCost, attr.TotalCost, attr.MetricCount, attr.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert cost attribution: %w", err)
	}
	// Reload to get conflict-updated id if any
	var out CostAttribution
	if err := m.db.GetContext(ctx, &out, `
		SELECT id, mission_id, mission_run_id, session_id,
		       compute_cost, storage_cost, network_cost, total_cost, metric_count, created_at
		FROM mission_cost_attributions WHERE mission_run_id = $1`, runID); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRunAttribution returns the cost snapshot for a run if present.
func (m *Manager) GetRunAttribution(ctx context.Context, missionID, runID uuid.UUID) (*CostAttribution, error) {
	var out CostAttribution
	err := m.db.GetContext(ctx, &out, `
		SELECT id, mission_id, mission_run_id, session_id,
		       compute_cost, storage_cost, network_cost, total_cost, metric_count, created_at
		FROM mission_cost_attributions
		WHERE mission_run_id = $1 AND mission_id = $2`, runID, missionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetMissionCostSummary sums attributions for a mission.
func (m *Manager) GetMissionCostSummary(ctx context.Context, missionID uuid.UUID) (*MissionCostSummary, error) {
	var out MissionCostSummary
	out.MissionID = missionID
	err := m.db.GetContext(ctx, &out, `
		SELECT
			$1::uuid AS mission_id,
			COUNT(*)::int AS run_count,
			COALESCE(SUM(compute_cost), 0) AS compute_cost,
			COALESCE(SUM(storage_cost), 0) AS storage_cost,
			COALESCE(SUM(network_cost), 0) AS network_cost,
			COALESCE(SUM(total_cost), 0) AS total_cost
		FROM mission_cost_attributions WHERE mission_id = $1`, missionID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
