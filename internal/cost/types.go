package cost

import (
	"time"

	"github.com/google/uuid"
)

// CostRate defines pricing for compute, storage, and network resources
type CostRate struct {
	ID                    uuid.UUID `db:"id" json:"id"`
	Name                  string    `db:"name" json:"name"`
	CPUCoreHourRate       float64   `db:"cpu_core_hour_rate" json:"cpu_core_hour_rate"`
	MemoryGBHourRate      float64   `db:"memory_gb_hour_rate" json:"memory_gb_hour_rate"`
	GPUHourRate           float64   `db:"gpu_hour_rate" json:"gpu_hour_rate"`
	StorageGBMonthRate    float64   `db:"storage_gb_month_rate" json:"storage_gb_month_rate"`
	SnapshotGBMonthRate   float64   `db:"snapshot_gb_month_rate" json:"snapshot_gb_month_rate"`
	ArtifactGBMonthRate   float64   `db:"artifact_gb_month_rate" json:"artifact_gb_month_rate"`
	EgressGBRate          float64   `db:"egress_gb_rate" json:"egress_gb_rate"`
	Active                bool      `db:"active" json:"active"`
	CreatedAt             time.Time `db:"created_at" json:"created_at"`
	UpdatedAt             time.Time `db:"updated_at" json:"updated_at"`
}

// CostMetric records resource usage and costs for a session over a time period
type CostMetric struct {
	ID              uuid.UUID  `db:"id" json:"id"`
	SessionID       uuid.UUID  `db:"session_id" json:"session_id"`
	PeriodStart     time.Time  `db:"period_start" json:"period_start"`
	PeriodEnd       time.Time  `db:"period_end" json:"period_end"`
	CPUCoreHours    float64    `db:"cpu_core_hours" json:"cpu_core_hours"`
	MemoryGBHours   float64    `db:"memory_gb_hours" json:"memory_gb_hours"`
	GPUHours        float64    `db:"gpu_hours" json:"gpu_hours"`
	WorkspaceGBDays float64    `db:"workspace_gb_days" json:"workspace_gb_days"`
	SnapshotGBDays  float64    `db:"snapshot_gb_days" json:"snapshot_gb_days"`
	ArtifactGBDays  float64    `db:"artifact_gb_days" json:"artifact_gb_days"`
	EgressGB        float64    `db:"egress_gb" json:"egress_gb"`
	IngressGB       float64    `db:"ingress_gb" json:"ingress_gb"`
	ComputeCost     float64    `db:"compute_cost" json:"compute_cost"`
	StorageCost     float64    `db:"storage_cost" json:"storage_cost"`
	NetworkCost     float64    `db:"network_cost" json:"network_cost"`
	TotalCost       float64    `db:"total_cost" json:"total_cost"`
	RateID          *uuid.UUID `db:"rate_id" json:"rate_id,omitempty"`
	Checksum        string     `db:"checksum" json:"checksum"`
	Signature       string     `db:"signature" json:"signature"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
}

// CostBudget defines spending limits for an organization
type CostBudget struct {
	ID              uuid.UUID  `db:"id" json:"id"`
	OrgID           uuid.UUID  `db:"org_id" json:"org_id"`
	MonthlyLimit    float64    `db:"monthly_limit" json:"monthly_limit"`
	AlertThreshold  float64    `db:"alert_threshold" json:"alert_threshold"`
	CurrentMonth    string     `db:"current_month" json:"current_month"`
	CurrentSpend    float64    `db:"current_spend" json:"current_spend"`
	AlertSent       bool       `db:"alert_sent" json:"alert_sent"`
	ExceededAt      *time.Time `db:"exceeded_at" json:"exceeded_at,omitempty"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updated_at"`
}

// CostAlert notifies about cost issues (idle sessions, budget warnings, etc.)
type CostAlert struct {
	ID               uuid.UUID  `db:"id" json:"id"`
	AlertType        string     `db:"alert_type" json:"alert_type"`
	Severity         string     `db:"severity" json:"severity"`
	SessionID        *uuid.UUID `db:"session_id" json:"session_id,omitempty"`
	OrgID            *uuid.UUID `db:"org_id" json:"org_id,omitempty"`
	Title            string     `db:"title" json:"title"`
	Description      string     `db:"description" json:"description"`
	PotentialSavings *float64   `db:"potential_savings" json:"potential_savings,omitempty"`
	Resolved         bool       `db:"resolved" json:"resolved"`
	ResolvedAt       *time.Time `db:"resolved_at" json:"resolved_at,omitempty"`
	ResolvedBy       *string    `db:"resolved_by" json:"resolved_by,omitempty"`
	CreatedAt        time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at" json:"updated_at"`
}

// Alert types
const (
	AlertTypeIdleSession     = "idle_session"
	AlertTypeBudgetWarning   = "budget_warning"
	AlertTypeBudgetExceeded  = "budget_exceeded"
	AlertTypeOptimization    = "optimization"
	AlertTypeStorageGrowth   = "storage_growth"
)

// Severity levels
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// SessionCostSummary aggregates all costs for a session
type SessionCostSummary struct {
	SessionID    uuid.UUID `json:"session_id"`
	SessionName  string    `json:"session_name"`
	ComputeCost  float64   `json:"compute_cost"`
	StorageCost  float64   `json:"storage_cost"`
	NetworkCost  float64   `json:"network_cost"`
	TotalCost    float64   `json:"total_cost"`
	FirstMetric  time.Time `json:"first_metric"`
	LastMetric   time.Time `json:"last_metric"`
}

// OrgCostSummary aggregates all costs for an organization
type OrgCostSummary struct {
	OrgID        uuid.UUID `json:"org_id"`
	OrgName      string    `json:"org_name"`
	ComputeCost  float64   `json:"compute_cost"`
	StorageCost  float64   `json:"storage_cost"`
	NetworkCost  float64   `json:"network_cost"`
	TotalCost    float64   `json:"total_cost"`
	SessionCount int       `json:"session_count"`
}

// CostBreakdown provides detailed cost analysis
type CostBreakdown struct {
	Period       string               `json:"period"` // YYYY-MM or YYYY-MM-DD
	ComputeCost  float64              `json:"compute_cost"`
	StorageCost  float64              `json:"storage_cost"`
	NetworkCost  float64              `json:"network_cost"`
	TotalCost    float64              `json:"total_cost"`
	TopSessions  []SessionCostSummary `json:"top_sessions"`
	AlertCount   int                  `json:"alert_count"`
}
