package cost

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrAlertNotFound is returned when an alert does not exist, or exists but is
// not visible to the caller. The two cases are deliberately indistinguishable.
var ErrAlertNotFound = errors.New("cost alert not found")

// ErrInvalidBudget is returned when budget input fails validation.
var ErrInvalidBudget = errors.New("invalid budget")

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
		FROM organizations o
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

// Scope restricts a cost query to the data one caller is allowed to see.
//
// Spend figures are business-sensitive: a deployment-wide total, and the names
// of the ten sessions that spent the most, tell any tenant how busy every other
// tenant is. Every aggregate query therefore takes a Scope, and only the master
// key gets the unrestricted view.
type Scope struct {
	// Master grants the deployment-wide view.
	Master bool
	// Actor is the API key principal whose sessions and orgs are visible.
	Actor string
}

// DeploymentScope returns the unrestricted scope used by the master key and by
// internal callers such as the cost sweeper.
func DeploymentScope() Scope { return Scope{Master: true} }

// ActorScope returns the scope visible to one API key principal.
func ActorScope(actor string) Scope { return Scope{Actor: actor} }

// sessionPredicate returns a SQL fragment restricting sessions aliased as
// `alias` to the scope, plus the argument to bind. An empty fragment means no
// restriction.
//
// Visibility mirrors Hub.checkSessionAccess: a principal sees the sessions it
// created plus every session belonging to an org it is a member of.
func (s Scope) sessionPredicate(alias string, argIndex int) (string, []interface{}) {
	if s.Master {
		return "", nil
	}
	frag := fmt.Sprintf(`AND (%[1]s.created_by = $%[2]d OR %[1]s.org_id IN (
			SELECT org_id FROM org_members WHERE principal = $%[2]d
		))`, alias, argIndex)
	return frag, []interface{}{s.Actor}
}

// GetCostBreakdown returns cost breakdown for a period, restricted to the
// sessions and alerts the scope may see.
func (t *Tracker) GetCostBreakdown(ctx context.Context, period string, scope Scope) (*CostBreakdown, error) {
	breakdown := &CostBreakdown{Period: period}

	periodStart := period + "-01"
	sessionFilter, scopeArgs := scope.sessionPredicate("s", 2)

	totalsArgs := append([]interface{}{periodStart}, scopeArgs...)
	err := t.db.GetContext(ctx, breakdown, fmt.Sprintf(`
		SELECT 
			COALESCE(SUM(cm.compute_cost), 0) as compute_cost,
			COALESCE(SUM(cm.storage_cost), 0) as storage_cost,
			COALESCE(SUM(cm.network_cost), 0) as network_cost,
			COALESCE(SUM(cm.total_cost), 0) as total_cost
		FROM cost_metrics cm
		JOIN sessions s ON s.id = cm.session_id
		WHERE cm.period_start >= $1::timestamptz
		AND cm.period_end < ($1::timestamptz + INTERVAL '1 month')
		%s
	`, sessionFilter), totalsArgs...)
	
	if err != nil {
		return nil, err
	}
	
	// Get top spending sessions
	err = t.db.SelectContext(ctx, &breakdown.TopSessions, fmt.Sprintf(`
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
		%s
		GROUP BY s.id, s.name
		ORDER BY total_cost DESC
		LIMIT 10
	`, sessionFilter), totalsArgs...)
	
	if err != nil {
		return nil, err
	}
	
	// Get alert count
	alertFilter, alertArgs := scope.alertPredicate(2)
	countArgs := append([]interface{}{periodStart}, alertArgs...)
	err = t.db.GetContext(ctx, &breakdown.AlertCount, fmt.Sprintf(`
		SELECT COUNT(*) FROM cost_alerts a
		WHERE NOT a.resolved
		AND a.created_at >= $1::timestamptz
		%s
	`, alertFilter), countArgs...)
	
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

// alertPredicate returns a SQL fragment restricting cost_alerts aliased as `a`
// to the scope, plus the argument to bind.
//
// An alert is visible when it names a session the principal can see, or an org
// it belongs to. Alerts carrying neither — deployment-wide notices — are only
// visible to the master key.
func (s Scope) alertPredicate(argIndex int) (string, []interface{}) {
	if s.Master {
		return "", nil
	}
	frag := fmt.Sprintf(`AND (
			a.session_id IN (
				SELECT id FROM sessions
				WHERE created_by = $%[1]d
				   OR org_id IN (SELECT org_id FROM org_members WHERE principal = $%[1]d)
			)
			OR a.org_id IN (SELECT org_id FROM org_members WHERE principal = $%[1]d)
		)`, argIndex)
	return frag, []interface{}{s.Actor}
}

// GetAlerts returns active cost alerts the scope may see.
func (t *Tracker) GetAlerts(ctx context.Context, resolved bool, scope Scope) ([]CostAlert, error) {
	filter, scopeArgs := scope.alertPredicate(2)
	args := append([]interface{}{resolved}, scopeArgs...)

	var alerts []CostAlert
	err := t.db.SelectContext(ctx, &alerts, fmt.Sprintf(`
		SELECT a.* FROM cost_alerts a
		WHERE a.resolved = $1
		%s
		ORDER BY a.created_at DESC
		LIMIT 100
	`, filter), args...)
	return alerts, err
}

// ResolveAlert marks an alert as resolved.
//
// The scope is part of the WHERE clause rather than a separate lookup: a
// caller must not be able to resolve — and so hide — an alert raised against
// another tenant's session. Returns ErrAlertNotFound when no visible alert
// matches, which also covers a wholly unknown ID.
func (t *Tracker) ResolveAlert(ctx context.Context, alertID uuid.UUID, resolvedBy string, scope Scope) error {
	filter, scopeArgs := scope.alertPredicate(4)
	args := append([]interface{}{alertID, time.Now(), resolvedBy}, scopeArgs...)

	res, err := t.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE cost_alerts a
		SET resolved = TRUE,
		    resolved_at = $2,
		    resolved_by = $3,
		    updated_at = $2
		WHERE a.id = $1
		%s
	`, filter), args...)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrAlertNotFound
	}
	return nil
}

// BudgetOpts describes a monthly spending limit for an org.
type BudgetOpts struct {
	MonthlyLimit   float64
	AlertThreshold float64
	Month          string // YYYY-MM
}

// MaxMonthlyLimit is the largest budget the schema can store: monthly_limit is
// DECIMAL(10,2), so a larger value would fail the insert with a numeric
// overflow rather than a useful validation error.
const MaxMonthlyLimit = 99_999_999.99

// validate bounds budget input.
//
// A negative or zero limit is rejected rather than stored: a stored negative
// limit reads as "already over budget forever", which would either silence
// budget alerts or fire them continuously depending on the comparison.
// A threshold outside (0, 1] has the same effect on the alert that derives
// from it.
func (o BudgetOpts) validate() error {
	switch {
	case math.IsNaN(o.MonthlyLimit) || math.IsInf(o.MonthlyLimit, 0):
		return fmt.Errorf("%w: monthly_limit must be a finite number", ErrInvalidBudget)
	case o.MonthlyLimit <= 0:
		return fmt.Errorf("%w: monthly_limit must be greater than 0", ErrInvalidBudget)
	case o.MonthlyLimit > MaxMonthlyLimit:
		return fmt.Errorf("%w: monthly_limit exceeds maximum of %.2f", ErrInvalidBudget, MaxMonthlyLimit)
	}

	if math.IsNaN(o.AlertThreshold) || o.AlertThreshold <= 0 || o.AlertThreshold > 1 {
		return fmt.Errorf("%w: alert_threshold must be greater than 0 and at most 1", ErrInvalidBudget)
	}

	if _, err := time.Parse("2006-01", o.Month); err != nil {
		return fmt.Errorf("%w: month must be in YYYY-MM format", ErrInvalidBudget)
	}

	return nil
}

// SetBudget creates or replaces the budget for an org and month.
func (t *Tracker) SetBudget(ctx context.Context, orgID uuid.UUID, opts BudgetOpts) error {
	if err := opts.validate(); err != nil {
		return err
	}

	_, err := t.db.ExecContext(ctx, `
		INSERT INTO cost_budgets (id, org_id, monthly_limit, alert_threshold, current_month)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id, current_month)
		DO UPDATE SET
			monthly_limit = EXCLUDED.monthly_limit,
			alert_threshold = EXCLUDED.alert_threshold,
			updated_at = NOW()
	`, uuid.New(), orgID, opts.MonthlyLimit, opts.AlertThreshold, opts.Month)
	return err
}

// GetBudget returns the budget for an org and month, or nil when none is set.
func (t *Tracker) GetBudget(ctx context.Context, orgID uuid.UUID, month string) (*CostBudget, error) {
	var budget CostBudget
	err := t.db.GetContext(ctx, &budget, `
		SELECT * FROM cost_budgets
		WHERE org_id = $1 AND current_month = $2
	`, orgID, month)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &budget, nil
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

// checksum computes a SHA-256 digest over every field that determines a cost
// metric's meaning.
//
// The digest must cover the raw usage counters, not just the derived totals:
// omitting them would let the usage that justifies a charge be rewritten while
// the totals — and therefore the digest — stayed valid. It also covers RateID,
// so a metric cannot be re-attributed to a cheaper rate card.
//
// Floats are formatted with strconv.FormatFloat('g', -1) rather than %f: %f
// truncates at six decimals, so two sub-microdollar amounts would collide.
// Fields are NUL-separated to prevent boundary confusion.
func (t *Tracker) checksum(m *CostMetric) string {
	h := sha256.New()

	writeField := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	writeFloat := func(f float64) {
		writeField(strconv.FormatFloat(f, 'g', -1, 64))
	}

	writeField(m.SessionID.String())
	writeField(m.PeriodStart.UTC().Format(time.RFC3339Nano))
	writeField(m.PeriodEnd.UTC().Format(time.RFC3339Nano))

	// Raw usage counters — these justify the charge.
	writeFloat(m.CPUCoreHours)
	writeFloat(m.MemoryGBHours)
	writeFloat(m.GPUHours)
	writeFloat(m.WorkspaceGBDays)
	writeFloat(m.SnapshotGBDays)
	writeFloat(m.ArtifactGBDays)
	writeFloat(m.EgressGB)
	writeFloat(m.IngressGB)

	// Derived costs.
	writeFloat(m.ComputeCost)
	writeFloat(m.StorageCost)
	writeFloat(m.NetworkCost)
	writeFloat(m.TotalCost)

	// The rate card the costs were computed against.
	if m.RateID != nil {
		writeField(m.RateID.String())
	} else {
		writeField("")
	}

	return hex.EncodeToString(h.Sum(nil))
}

// sign computes the HMAC over the checksum. Signing the digest rather than the
// fields again keeps the two in lockstep: any field change alters the checksum,
// which alters the signature.
func (t *Tracker) sign(m *CostMetric) string {
	if len(t.signingKey) == 0 {
		return ""
	}

	h := hmac.New(sha256.New, t.signingKey)
	h.Write([]byte(m.Checksum))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyMetric reports whether a metric's stored checksum and signature match
// its contents. Returns false when signing is not configured so that an
// unkeyed deployment cannot treat every metric as authentic.
func (t *Tracker) VerifyMetric(m *CostMetric) bool {
	if len(t.signingKey) == 0 || m.Signature == "" || m.Checksum == "" {
		return false
	}

	if t.checksum(m) != m.Checksum {
		return false
	}

	got, err := hex.DecodeString(m.Signature)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(t.sign(m))
	if err != nil {
		return false
	}

	return hmac.Equal(got, want)
}
