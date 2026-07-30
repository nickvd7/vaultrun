-- Cost Intelligence: Real-time cost tracking for sessions
-- See: docs/features/cost-intelligence.md

-- Cost rates configuration (cloud provider pricing)
CREATE TABLE cost_rates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,              -- e.g. "AWS us-east-1", "GCP europe-west1"
    
    -- Compute rates (per hour)
    cpu_core_hour_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.04,    -- $ per CPU core-hour
    memory_gb_hour_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.005,  -- $ per GB-hour
    gpu_hour_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.90,         -- $ per GPU-hour
    
    -- Storage rates (per GB-month)
    storage_gb_month_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.023, -- $ per GB-month (workspace)
    snapshot_gb_month_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.05, -- $ per GB-month (snapshots)
    artifact_gb_month_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.023, -- $ per GB-month (artifacts)
    
    -- Network rates (per GB)
    egress_gb_rate DECIMAL(10, 6) NOT NULL DEFAULT 0.09,        -- $ per GB egress
    
    -- Metadata
    active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(name)
);

-- Insert default AWS pricing
INSERT INTO cost_rates (name, cpu_core_hour_rate, memory_gb_hour_rate, gpu_hour_rate, 
                        storage_gb_month_rate, snapshot_gb_month_rate, egress_gb_rate)
VALUES ('default', 0.04, 0.005, 0.90, 0.023, 0.05, 0.09);

-- Cost metrics per session (immutable records for audit trail)
CREATE TABLE cost_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    
    -- Time period this metric covers
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    
    -- Compute metrics
    cpu_core_hours DECIMAL(12, 6) DEFAULT 0,   -- Total CPU core-hours in period
    memory_gb_hours DECIMAL(12, 6) DEFAULT 0,  -- Total GB-hours in period
    gpu_hours DECIMAL(12, 6) DEFAULT 0,        -- Total GPU-hours in period
    
    -- Storage metrics (average size * period duration)
    workspace_gb_days DECIMAL(12, 6) DEFAULT 0,
    snapshot_gb_days DECIMAL(12, 6) DEFAULT 0,
    artifact_gb_days DECIMAL(12, 6) DEFAULT 0,
    
    -- Network metrics
    egress_gb DECIMAL(12, 6) DEFAULT 0,        -- Data transferred out
    ingress_gb DECIMAL(12, 6) DEFAULT 0,       -- Data transferred in (usually free)
    
    -- Calculated costs (in USD)
    compute_cost DECIMAL(10, 4) DEFAULT 0,
    storage_cost DECIMAL(10, 4) DEFAULT 0,
    network_cost DECIMAL(10, 4) DEFAULT 0,
    total_cost DECIMAL(10, 4) DEFAULT 0,
    
    -- Rate snapshot (for historical accuracy)
    rate_id UUID REFERENCES cost_rates(id),
    
    -- Security: HMAC signature for immutability
    checksum VARCHAR(128) NOT NULL,
    signature VARCHAR(128) NOT NULL,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    -- Prevent overlapping metrics for same session
    CONSTRAINT cost_metrics_period_unique UNIQUE(session_id, period_start)
);

CREATE INDEX idx_cost_metrics_session ON cost_metrics(session_id, period_start DESC);
CREATE INDEX idx_cost_metrics_period ON cost_metrics(period_start DESC, period_end DESC);
CREATE INDEX idx_cost_metrics_created ON cost_metrics(created_at DESC);

-- Prevent updates to cost metrics (immutable audit trail)
CREATE OR REPLACE FUNCTION prevent_cost_metric_update()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'cost_metrics records are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cost_metrics_immutable
    BEFORE UPDATE ON cost_metrics
    FOR EACH ROW
    EXECUTE FUNCTION prevent_cost_metric_update();

-- Budget management per organization
CREATE TABLE cost_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    
    -- Budget limits
    monthly_limit DECIMAL(10, 2) NOT NULL,     -- $ per month
    alert_threshold DECIMAL(5, 2) DEFAULT 0.8,  -- Alert at 80%
    
    -- Current period tracking
    current_month VARCHAR(7) NOT NULL,          -- YYYY-MM format
    current_spend DECIMAL(10, 2) DEFAULT 0,
    
    -- Notifications
    alert_sent BOOLEAN DEFAULT FALSE,
    exceeded_at TIMESTAMPTZ,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(org_id, current_month)
);

CREATE INDEX idx_cost_budgets_org ON cost_budgets(org_id, current_month);

-- Cost alerts (idle sessions, budget warnings, optimization opportunities)
CREATE TABLE cost_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    
    -- Alert type
    alert_type VARCHAR(50) NOT NULL,  -- idle_session, budget_warning, optimization, storage_growth
    severity VARCHAR(20) NOT NULL,    -- info, warning, critical
    
    -- Context
    session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
    org_id UUID REFERENCES orgs(id) ON DELETE CASCADE,
    
    -- Alert details
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    potential_savings DECIMAL(10, 2),  -- $ per month
    
    -- Resolution
    resolved BOOLEAN DEFAULT FALSE,
    resolved_at TIMESTAMPTZ,
    resolved_by TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_cost_alerts_unresolved ON cost_alerts(created_at DESC) WHERE NOT resolved;
CREATE INDEX idx_cost_alerts_session ON cost_alerts(session_id) WHERE NOT resolved;
CREATE INDEX idx_cost_alerts_org ON cost_alerts(org_id) WHERE NOT resolved;

-- Add cost tracking columns to sessions
ALTER TABLE sessions ADD COLUMN total_cost DECIMAL(10, 4) DEFAULT 0;
ALTER TABLE sessions ADD COLUMN last_cost_update TIMESTAMPTZ;

-- Audit events for cost operations
-- ActionCostMetricCreated = "cost.metric.created"
-- ActionBudgetExceeded = "cost.budget.exceeded"
-- ActionAlertCreated = "cost.alert.created"
