package cost

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Tracker collects and calculates cost metrics for sessions
type Tracker struct {
	db         *sqlx.DB
	signingKey []byte
}

// New creates a new cost Tracker
func New(db *sqlx.DB, signingKey []byte) *Tracker {
	return &Tracker{
		db:         db,
		signingKey: signingKey,
	}
}

// RecordMetric records a cost metric for a session
func (t *Tracker) RecordMetric(ctx context.Context, metric *CostMetric) error {
	// Get active cost rate
	rate, err := t.getActiveRate(ctx)
	if err != nil {
		return fmt.Errorf("get cost rate: %w", err)
	}
	
	metric.RateID = &rate.ID
	
	// Calculate costs
	metric.ComputeCost = t.calculateComputeCost(metric, rate)
	metric.StorageCost = t.calculateStorageCost(metric, rate)
	metric.NetworkCost = t.calculateNetworkCost(metric, rate)
	metric.TotalCost = metric.ComputeCost + metric.StorageCost + metric.NetworkCost
	
	// Generate checksum and signature
	metric.Checksum = t.checksum(metric)
	metric.Signature = t.sign(metric)
	
	// Insert metric
	_, err = t.db.NamedExecContext(ctx, `
		INSERT INTO cost_metrics (
			id, session_id, period_start, period_end,
			cpu_core_hours, memory_gb_hours, gpu_hours,
			workspace_gb_days, snapshot_gb_days, artifact_gb_days,
			egress_gb, ingress_gb,
			compute_cost, storage_cost, network_cost, total_cost,
			rate_id, checksum, signature, created_at
		) VALUES (
			:id, :session_id, :period_start, :period_end,
			:cpu_core_hours, :memory_gb_hours, :gpu_hours,
			:workspace_gb_days, :snapshot_gb_days, :artifact_gb_days,
			:egress_gb, :ingress_gb,
			:compute_cost, :storage_cost, :network_cost, :total_cost,
			:rate_id, :checksum, :signature, :created_at
		)
	`, metric)
	
	if err != nil {
		return fmt.Errorf("insert metric: %w", err)
	}
	
	// Update session total cost
	_, err = t.db.ExecContext(ctx, `
		UPDATE sessions 
		SET total_cost = (
			SELECT COALESCE(SUM(total_cost), 0)
			FROM cost_metrics
			WHERE session_id = $1
		),
		last_cost_update = NOW()
		WHERE id = $1
	`, metric.SessionID)
	
	return err
}

// GetSessionCosts returns all cost metrics for a session
func (t *Tracker) GetSessionCosts(ctx context.Context, sessionID uuid.UUID) ([]CostMetric, error) {
	var metrics []CostMetric
	err := t.db.SelectContext(ctx, &metrics, `
		SELECT * FROM cost_metrics
		WHERE session_id = $1
		ORDER BY period_start DESC
	`, sessionID)
	return metrics, err
}

// GetSessionSummary returns aggregated cost summary for a session
func (t *Tracker) GetSessionSummary(ctx context.Context, sessionID uuid.UUID) (*SessionCostSummary, error) {
	var summary SessionCostSummary
	err := t.db.GetContext(ctx, &summary, `
		SELECT 
			s.id as session_id,
			COALESCE(s.name, s.id::text) as session_name,
			COALESCE(SUM(cm.compute_cost), 0) as compute_cost,
			COALESCE(SUM(cm.storage_cost), 0) as storage_cost,
			COALESCE(SUM(cm.network_cost), 0) as network_cost,
			COALESCE(SUM(cm.total_cost), 0) as total_cost,
			MIN(cm.period_start) as first_metric,
			MAX(cm.period_end) as last_metric
		FROM sessions s
		LEFT JOIN cost_metrics cm ON cm.session_id = s.id
		WHERE s.id = $1
		GROUP BY s.id, s.name
	`, sessionID)
	
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// GetOrgSummary returns aggregated cost summary for an organization
func (t *Tracker) GetOrgSummary(ctx context.Context, orgID uuid.UUID, month string) (*OrgCostSummary, error) {
	var summary OrgCostSummary
	err := t.db.GetContext(ctx, &summary, `
		SELECT 
			o.id as org_id,
			o.name as org_name,
			COALESCE(SUM(cm.compute_cost), 0) as compute_cost,
			COALESCE(SUM(cm.storage_cost), 0) as storage_cost,
			COALESCE(SUM(cm.network_cost), 0) as network_cost,
			COALESCE(SUM(cm.total_cost), 0) as total_cost,
			COUNT(DISTINCT s.id) as session_count
		FROM orgs o
		LEFT JOIN sessions s ON s.org_id = o.id
		LEFT JOIN cost_metrics cm ON cm.session_id = s.id 
			AND cm.period_start >= $2::timestamptz
			AND cm.period_end < ($2::timestamptz + INTERVAL '1 month')
		WHERE o.id = $1
		GROUP BY o.id, o.name
	`, orgID, month+"-01")
	
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// GetCostBreakdown returns cost breakdown for a period
func (t *Tracker) GetCostBreakdown(ctx context.Context, period string) (*CostBreakdown, error) {
	breakdown := &CostBreakdown{Period: period}
	
	// Get total costs for period
	err := t.db.GetContext(ctx, breakdown, `
		SELECT 
			COALESCE(SUM(compute_cost), 0) as compute_cost,
			COALESCE(SUM(storage_cost), 0) as storage_cost,
			COALESCE(SUM(network_cost), 0) as network_cost,
			COALESCE(SUM(total_cost), 0) as total_cost
		FROM cost_metrics
		WHERE period_start >= $1::timestamptz
		AND period_end < ($1::timestamptz + INTERVAL '1 month')
	`, period+"-01")
	
	if err != nil {
		return nil, err
	}
	
	// Get top spending sessions
	err = t.db.SelectContext(ctx, &breakdown.TopSessions, `
		SELECT 
			s.id as session_id,
			COALESCE(s.name, s.id::text) as session_name,
			COALESCE(SUM(cm.compute_cost), 0) as compute_cost,
			COALESCE(SUM(cm.storage_cost), 0) as storage_cost,
			COALESCE(SUM(cm.network_cost), 0) as network_cost,
			COALESCE(SUM(cm.total_cost), 0) as total_cost,
			MIN(cm.period_start) as first_metric,
			MAX(cm.period_end) as last_metric
		FROM sessions s
		INNER JOIN cost_metrics cm ON cm.session_id = s.id
		WHERE cm.period_start >= $1::timestamptz
		AND cm.period_end < ($1::timestamptz + INTERVAL '1 month')
		GROUP BY s.id, s.name
		ORDER BY total_cost DESC
		LIMIT 10
	`, period+"-01")
	
	if err != nil {
		return nil, err
	}
	
	// Get alert count
	err = t.db.GetContext(ctx, &breakdown.AlertCount, `
		SELECT COUNT(*) FROM cost_alerts
		WHERE NOT resolved
		AND created_at >= $1::timestamptz
	`, period+"-01")
	
	return breakdown, err
}

// CreateAlert creates a cost alert
func (t *Tracker) CreateAlert(ctx context.Context, alert *CostAlert) error {
	if alert.ID == uuid.Nil {
		alert.ID = uuid.New()
	}
	
	_, err := t.db.NamedExecContext(ctx, `
		INSERT INTO cost_alerts (
			id, alert_type, severity, session_id, org_id,
			title, description, potential_savings,
			resolved, created_at, updated_at
		) VALUES (
			:id, :alert_type, :severity, :session_id, :org_id,
			:title, :description, :potential_savings,
			:resolved, NOW(), NOW()
		)
	`, alert)
	
	return err
}

// GetAlerts returns active cost alerts
func (t *Tracker) GetAlerts(ctx context.Context, resolved bool) ([]CostAlert, error) {
	var alerts []CostAlert
	err := t.db.SelectContext(ctx, &alerts, `
		SELECT * FROM cost_alerts
		WHERE resolved = $1
		ORDER BY created_at DESC
		LIMIT 100
	`, resolved)
	return alerts, err
}

// ResolveAlert marks an alert as resolved
func (t *Tracker) ResolveAlert(ctx context.Context, alertID uuid.UUID, resolvedBy string) error {
	now := time.Now()
	_, err := t.db.ExecContext(ctx, `
		UPDATE cost_alerts
		SET resolved = TRUE,
		    resolved_at = $2,
		    resolved_by = $3,
		    updated_at = $2
		WHERE id = $1
	`, alertID, now, resolvedBy)
	return err
}

// Helper methods

func (t *Tracker) getActiveRate(ctx context.Context) (*CostRate, error) {
	var rate CostRate
	err := t.db.GetContext(ctx, &rate, `
		SELECT * FROM cost_rates WHERE active = TRUE LIMIT 1
	`)
	return &rate, err
}

func (t *Tracker) calculateComputeCost(m *CostMetric, rate *CostRate) float64 {
	cpuCost := m.CPUCoreHours * rate.CPUCoreHourRate
	memoryCost := m.MemoryGBHours * rate.MemoryGBHourRate
	gpuCost := m.GPUHours * rate.GPUHourRate
	return cpuCost + memoryCost + gpuCost
}

func (t *Tracker) calculateStorageCost(m *CostMetric, rate *CostRate) float64 {
	// Convert GB-days to GB-months (assuming 30 days/month)
	workspaceCost := (m.WorkspaceGBDays / 30.0) * rate.StorageGBMonthRate
	snapshotCost := (m.SnapshotGBDays / 30.0) * rate.SnapshotGBMonthRate
	artifactCost := (m.ArtifactGBDays / 30.0) * rate.ArtifactGBMonthRate
	return workspaceCost + snapshotCost + artifactCost
}

func (t *Tracker) calculateNetworkCost(m *CostMetric, rate *CostRate) float64 {
	// Egress is charged, ingress is usually free
	return m.EgressGB * rate.EgressGBRate
}

func (t *Tracker) checksum(m *CostMetric) string {
	data := fmt.Sprintf("%s:%s:%s:%f:%f:%f",
		m.SessionID.String(),
		m.PeriodStart.Format(time.RFC3339),
		m.PeriodEnd.Format(time.RFC3339),
		m.TotalCost,
		m.ComputeCost,
		m.StorageCost,
	)
	h := sha256.New()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func (t *Tracker) sign(m *CostMetric) string {
	data := m.Checksum
	h := hmac.New(sha256.New, t.signingKey)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}
