# Cost Intelligence Dashboard

## Executive Summary

Real-time kosten tracking voor self-hosted VaultRun deployments. Track compute, storage, en netwerk kosten per session/org/user. Krijg idle session alerts, resource optimization recommendations, en budget forecasting.

**Status:** 📈 Prioriteit 2  
**Effort:** Medium (2-3 weken)  
**Dependencies:** Metrics collection infrastructure (Prometheus/InfluxDB)

---

## Problem Statement

### Self-hosted = You Pay The Bill

Met SaaS betaal je per gebruik. Met self-hosted VaultRun:
- **Jij** betaalt voor EC2/GCE instances
- **Jij** betaalt voor S3/GCS storage  
- **Jij** betaalt voor data transfer
- **Jij** hebt geen zicht op kosten per session/team/project

**Current Pain Points:**

1. **No visibility** — "How much kost deze agent run?"
2. **Resource waste** — Idle sessions blijven draaien ($$$ verlies)
3. **No optimization** — Sessions alloceren 4GB maar gebruiken 200MB
4. **No budgets** — Teams hebben geen spending limiet
5. **No forecasting** — "Hoeveel gaat dit project kosten?"

**Impact:** Operators hebben geen kostenbewustzijn. Cloud bill explodes. CFO is not happy.

---

## Solution Overview

### Features

1. **Real-time cost tracking** — See costs per session, org, user
2. **Idle detection** — Alert when sessions waste resources
3. **Right-sizing recommendations** — "Reduce this session to 1 CPU, save 50%"
4. **Budget management** — Set spending limits per org/project
5. **Cost forecasting** — Predict monthly costs based on trends
6. **Cost breakdown** — Compute vs storage vs network
7. **Export & reporting** — CSV/PDF reports voor finance teams

### Dashboard

```
┌────────────────────────────────────────────────────────┐
│ Cost Dashboard                    This Month: $1,247   │
├────────────────────────────────────────────────────────┤
│                                                        │
│  📊 Cost Breakdown                                     │
│  ├─ Compute:   $847  (68%)  ████████████░░░░          │
│  ├─ Storage:   $312  (25%)  ████░░░░░░░░░░░          │
│  └─ Network:    $88   (7%)  █░░░░░░░░░░░░░░          │
│                                                        │
│  📈 Top Spending Sessions                              │
│  1. ml-training-gpu    $214   [💡 Idle 8h]           │
│  2. data-pipeline      $156   [✓ Active]              │
│  3. web-scraper        $89    [💡 Over-provisioned]   │
│                                                        │
│  ⚠️ Alerts (3)                                         │
│  • Session 'debug-2024' idle for 12 hours ($24/day)   │
│  • Org 'data-team' at 87% of monthly budget           │
│  • Snapshot storage grew 40% this week                │
│                                                        │
│  💡 Recommendations                                     │
│  • Delete 12 idle sessions → Save $156/month          │
│  • Right-size 8 over-provisioned sessions → Save $89  │
│  • Enable snapshot pruning → Save $45/month           │
└────────────────────────────────────────────────────────┘
```

---

## Architecture

### Cost Model

```go
type SessionCost struct {
    SessionID       uuid.UUID
    
    // Compute costs
    CPUCoreHours    float64  // Total CPU core-hours
    MemoryGBHours   float64  // Total GB-hours
    GPUHours        float64  // If GPU enabled
    
    // Storage costs
    WorkspaceGB     float64  // Current workspace size
    SnapshotGB      float64  // Snapshots size
    ArtifactGB      float64  // Artifacts size
    StorageGBDays   float64  // GB-days (avg size * days)
    
    // Network costs
    EgressGB        float64  // Data transferred out
    IngressGB       float64  // Data transferred in (usually free)
    
    // Derived costs (in USD)
    ComputeCost     float64
    StorageCost     float64
    NetworkCost     float64
    TotalCost       float64
    
    // Time tracking
    CreatedAt       time.Time
    LastActive      time.Time
    TotalUptime     time.Duration
    IdleTime        time.Duration
}

type CostRate struct {
    // Per hour rates
    CPUCoreHourRate    float64  // e.g. $0.04 per core-hour (AWS t3.medium)
    MemoryGBHourRate   float64  // e.g. $0.005 per GB-hour
    GPUHourRate        float64  // e.g. $0.90 per hour (AWS p3.2xlarge)
    
    // Per GB-month rates
    StorageGBMonthRate float64  // e.g. $0.023 per GB-month (S3 Standard)
    SnapshotGBMonthRate float64 // e.g. $0.05 per GB-month (EBS snapshots)
    
    // Per GB transfer
    EgressGBRate       float64  // e.g. $0.09 per GB (AWS data transfer out)
}
```

### Data Collection

**Metrics Collection Points:**

1. **Container stats** — CPU/memory usage per session
   - Polled every 30 seconds via Docker stats API
   - Aggregated to hourly compute cost

2. **Storage metrics** — Workspace/snapshot/artifact sizes
   - Checked hourly
   - Converted to GB-days for monthly cost

3. **Network metrics** — Traffic in/out per session
   - From Docker network stats
   - Aggregated to total egress GB

**Storage:**

```sql
-- New table: cost_metrics
CREATE TABLE cost_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    org_id UUID REFERENCES orgs(id) ON DELETE SET NULL,
    
    -- Time period
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    
    -- Compute metrics
    cpu_core_hours DECIMAL(10, 4),
    memory_gb_hours DECIMAL(10, 4),
    gpu_hours DECIMAL(10, 4),
    
    -- Storage metrics
    workspace_gb_days DECIMAL(10, 4),
    snapshot_gb_days DECIMAL(10, 4),
    artifact_gb_days DECIMAL(10, 4),
    
    -- Network metrics
    egress_gb DECIMAL(10, 4),
    ingress_gb DECIMAL(10, 4),
    
    -- Costs (USD)
    compute_cost DECIMAL(10, 4),
    storage_cost DECIMAL(10, 4),
    network_cost DECIMAL(10, 4),
    total_cost DECIMAL(10, 4),
    
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_cost_metrics_session ON cost_metrics(session_id, period_start DESC);
CREATE INDEX idx_cost_metrics_org ON cost_metrics(org_id, period_start DESC);
CREATE INDEX idx_cost_metrics_period ON cost_metrics(period_start DESC);

-- New table: cost_budgets
CREATE TABLE cost_budgets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES orgs(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,  -- Nullable (org-level budget)
    
    -- Budget config
    name VARCHAR(255) NOT NULL,
    amount_usd DECIMAL(10, 2) NOT NULL,
    period VARCHAR(20) NOT NULL,  -- 'daily', 'weekly', 'monthly'
    
    -- Alerts
    alert_threshold DECIMAL(5, 2),  -- e.g. 0.80 = 80%
    alert_enabled BOOLEAN DEFAULT true,
    
    -- Tracking
    current_spend DECIMAL(10, 2) DEFAULT 0.0,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

---

## Implementation Plan

### Phase 1: Metrics Collection (Week 1)

- [ ] Implement `internal/cost/collector.go` — Poll Docker stats
- [ ] Background goroutine: collect metrics every 30s
- [ ] Aggregate to hourly cost entries
- [ ] Store in `cost_metrics` table

### Phase 2: Cost Calculator (Week 1-2)

- [ ] Implement `internal/cost/calculator.go`
- [ ] Configurable cost rates (env vars)
- [ ] Calculate compute/storage/network costs
- [ ] API endpoint: `GET /api/v1/sessions/:id/cost`

### Phase 3: Analytics & Optimization (Week 2)

- [ ] Idle session detection (no activity for X hours)
- [ ] Over-provisioning detection (allocated >> used)
- [ ] Right-sizing recommendations
- [ ] API endpoint: `GET /api/v1/cost/recommendations`

### Phase 4: Budgets & Alerts (Week 2-3)

- [ ] Budget management (CRUD endpoints)
- [ ] Real-time budget tracking
- [ ] Alert when budget threshold exceeded
- [ ] Email/Slack notifications

### Phase 5: Dashboard UI (Week 3)

- [ ] Cost overview page
- [ ] Session cost breakdown
- [ ] Org-level cost analytics
- [ ] Budget management UI
- [ ] Export reports (CSV/PDF)

---

## API Examples

### Get Session Cost

```bash
GET /api/v1/sessions/sess_abc123/cost

Response:
{
  "session_id": "sess_abc123",
  "name": "ml-training",
  "uptime_hours": 8.5,
  "idle_hours": 3.2,
  
  "compute": {
    "cpu_core_hours": 17.0,
    "memory_gb_hours": 68.0,
    "gpu_hours": 8.5,
    "cost_usd": 7.92
  },
  
  "storage": {
    "workspace_gb": 2.4,
    "snapshots_gb": 8.1,
    "artifacts_gb": 0.3,
    "cost_usd": 0.12
  },
  
  "network": {
    "egress_gb": 1.2,
    "ingress_gb": 0.8,
    "cost_usd": 0.11
  },
  
  "total_cost_usd": 8.15,
  "cost_per_hour_usd": 0.96,
  
  "recommendations": [
    {
      "type": "idle",
      "message": "Session has been idle for 3.2 hours. Consider stopping it.",
      "potential_savings_usd": 3.07
    }
  ]
}
```

### Get Cost Breakdown by Org

```bash
GET /api/v1/cost/organizations/org_123?period=month

Response:
{
  "org_id": "org_123",
  "org_name": "Data Team",
  "period": "2026-07",
  
  "total_cost_usd": 1247.32,
  
  "breakdown": {
    "compute": 847.12,
    "storage": 312.45,
    "network": 87.75
  },
  
  "top_sessions": [
    {
      "session_id": "sess_001",
      "name": "ml-training-gpu",
      "cost_usd": 214.50,
      "status": "running",
      "idle_hours": 8.0
    },
    {
      "session_id": "sess_002",
      "name": "data-pipeline",
      "cost_usd": 156.20,
      "status": "running",
      "idle_hours": 0.0
    }
  ],
  
  "budget": {
    "amount_usd": 1500.0,
    "remaining_usd": 252.68,
    "percent_used": 83.2,
    "days_remaining": 9
  }
}
```

### Get Optimization Recommendations

```bash
GET /api/v1/cost/recommendations?org_id=org_123

Response:
{
  "recommendations": [
    {
      "type": "idle_sessions",
      "severity": "high",
      "message": "12 sessions have been idle for >6 hours",
      "potential_savings_usd": 156.00,
      "sessions": ["sess_001", "sess_003", ...],
      "action": "delete_sessions"
    },
    {
      "type": "over_provisioned",
      "severity": "medium",
      "message": "8 sessions use <25% of allocated resources",
      "potential_savings_usd": 89.50,
      "sessions": ["sess_004", "sess_007", ...],
      "action": "right_size"
    },
    {
      "type": "snapshot_bloat",
      "severity": "low",
      "message": "Snapshot storage grew 40% this week",
      "potential_savings_usd": 45.00,
      "action": "enable_snapshot_pruning"
    }
  ],
  
  "total_potential_savings_usd": 290.50
}
```

---

## Configuration

### Cost Rates (Environment Variables)

```bash
# Compute rates (per hour)
COST_CPU_CORE_HOUR=0.04      # $0.04 per CPU core-hour
COST_MEMORY_GB_HOUR=0.005    # $0.005 per GB-hour
COST_GPU_HOUR=0.90           # $0.90 per GPU-hour

# Storage rates (per GB-month)
COST_STORAGE_GB_MONTH=0.023   # S3 Standard
COST_SNAPSHOT_GB_MONTH=0.05   # EBS snapshots
COST_ARTIFACT_GB_MONTH=0.023  # S3 Standard

# Network rates (per GB)
COST_EGRESS_GB=0.09           # AWS data transfer out

# Metrics collection
COST_METRICS_INTERVAL=30s     # How often to collect stats
COST_METRICS_AGGREGATION=1h   # Aggregate to hourly entries

# Alerts
COST_IDLE_THRESHOLD=6h        # Alert if idle >6 hours
COST_ALERT_EMAIL=ops@company.com
COST_ALERT_SLACK_WEBHOOK=https://...
```

### Pre-configured Rate Profiles

```bash
# Quick config for common cloud providers
COST_PROFILE=aws-us-east-1    # Use AWS US-East-1 rates
# or
COST_PROFILE=gcp-us-central1  # Use GCP US-Central1 rates
# or
COST_PROFILE=azure-eastus     # Use Azure East US rates
```

---

## MCP Tools

### New Tools

```go
{
    Name:        "cost_get_session_cost",
    Description: "Get cost breakdown for a session",
    InputSchema: {
        "session_id": "string (required)",
    },
}

{
    Name:        "cost_get_organization_cost",
    Description: "Get total costs for an organization",
    InputSchema: {
        "org_id": "string (required)",
        "period": "string (optional) - month|week|day",
    },
}

{
    Name:        "cost_get_recommendations",
    Description: "Get cost optimization recommendations",
    InputSchema: {
        "org_id": "string (optional)",
    },
}

{
    Name:        "cost_create_budget",
    Description: "Create a spending budget",
    InputSchema: {
        "org_id": "string (required)",
        "amount_usd": "number (required)",
        "period": "string (required) - daily|weekly|monthly",
        "alert_threshold": "number (optional) - 0.0-1.0",
    },
}
```

---

## Dashboard UI

### Cost Overview Page

```
┌──────────────────────────────────────────────────────────┐
│ Cost Intelligence                                         │
├──────────────────────────────────────────────────────────┤
│                                                          │
│  This Month: $1,247   ↑ 15% vs last month               │
│                                                          │
│  ┌────────────────────────────────────────────────────┐ │
│  │                                                    │ │
│  │    Cost Trend (30 days)                           │ │
│  │    $60  ┌─────┐                                   │ │
│  │        │     │                                    │ │
│  │    $40 ├─────┤                                   │ │
│  │        │     │  ┌─────┐                          │ │
│  │    $20 ├─────┼──┤     │                          │ │
│  │        └─────┴──┴─────┴──────────                │ │
│  │        Week1  Week2  Week3  Week4                 │ │
│  │                                                    │ │
│  └────────────────────────────────────────────────────┘ │
│                                                          │
│  Breakdown:                                              │
│  • Compute:   $847  (68%)  ████████████████░░░░         │
│  • Storage:   $312  (25%)  ██████░░░░░░░░░░░░░         │
│  • Network:    $88   (7%)  ██░░░░░░░░░░░░░░░░░         │
│                                                          │
│  [📥 Export CSV] [📄 Generate Report] [⚙️ Settings]     │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│ Top Spending Sessions                                    │
├──────────────────────────────────────────────────────────┤
│  Session               Cost    Status    Actions         │
│  ml-training-gpu      $214    ⚠️ Idle    [Stop] [Resize]│
│  data-pipeline        $156    ✓ Active   [View]          │
│  web-scraper          $89     💡 Over    [Right-size]   │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│ 💡 Recommendations (Save $290/month)                      │
├──────────────────────────────────────────────────────────┤
│  🔴 High Priority                                        │
│  • Delete 12 idle sessions → Save $156                   │
│                                                          │
│  🟡 Medium Priority                                      │
│  • Right-size 8 over-provisioned sessions → Save $89     │
│                                                          │
│  🟢 Low Priority                                         │
│  • Enable snapshot pruning → Save $45                    │
└──────────────────────────────────────────────────────────┘
```

---

## Optimization Recommendations

### 1. Idle Session Detection

**Algorithm:**

```go
func (c *CostOptimizer) DetectIdle(session *Session, idleThreshold time.Duration) *Recommendation {
    lastActivity := session.LastCommandAt
    if lastActivity.IsZero() {
        lastActivity = session.CreatedAt
    }
    
    idleTime := time.Since(lastActivity)
    if idleTime < idleThreshold {
        return nil  // Not idle
    }
    
    // Calculate cost of idle time
    hourlyRate := c.calculateHourlyRate(session)
    wastedCost := hourlyRate * (idleTime.Hours())
    
    return &Recommendation{
        Type:     "idle",
        Severity: "high",
        Message:  fmt.Sprintf("Session idle for %s", idleTime.Round(time.Minute)),
        Savings:  wastedCost,
        Action:   "delete_session",
    }
}
```

### 2. Over-Provisioning Detection

**Algorithm:**

```go
func (c *CostOptimizer) DetectOverProvisioned(session *Session) *Recommendation {
    stats := c.getAverageStats(session)
    
    cpuUtil := stats.AvgCPU / session.CPULimit
    memUtil := stats.AvgMemory / session.MemoryLimitMB
    
    if cpuUtil < 0.25 && memUtil < 0.25 {
        // Using <25% of resources
        recommendedCPU := math.Ceil(stats.AvgCPU * 1.5)  // 50% buffer
        recommendedMem := math.Ceil(stats.AvgMemory * 1.5)
        
        currentCost := c.calculateSessionCost(session)
        optimizedCost := c.calculateCost(recommendedCPU, recommendedMem, session.Uptime)
        savings := currentCost - optimizedCost
        
        return &Recommendation{
            Type:     "over_provisioned",
            Severity: "medium",
            Message:  fmt.Sprintf("Using only %.0f%% CPU and %.0f%% memory", cpuUtil*100, memUtil*100),
            Savings:  savings,
            Action:   "right_size",
            SuggestedLimits: &ResourceLimits{
                CPU:    recommendedCPU,
                Memory: recommendedMem,
            },
        }
    }
    
    return nil
}
```

### 3. Snapshot Bloat Detection

```go
func (c *CostOptimizer) DetectSnapshotBloat(orgID uuid.UUID) *Recommendation {
    snapshots := c.getSnapshots(orgID)
    
    totalSize := sumSizes(snapshots)
    oldSnapshots := filterOlderThan(snapshots, 30*24*time.Hour)  // >30 days old
    oldSize := sumSizes(oldSnapshots)
    
    if oldSize > totalSize * 0.3 {  // >30% of snapshots are old
        monthlyCost := oldSize * c.rates.SnapshotGBMonthRate
        
        return &Recommendation{
            Type:     "snapshot_bloat",
            Severity: "low",
            Message:  fmt.Sprintf("%.1f GB of snapshots are >30 days old", oldSize),
            Savings:  monthlyCost,
            Action:   "enable_snapshot_pruning",
        }
    }
    
    return nil
}
```

---

## Budget Management

### Budget Alerts

```go
type BudgetChecker struct {
    db    *sql.DB
    mailer Mailer
    slack  SlackClient
}

func (b *BudgetChecker) CheckBudgets(ctx context.Context) {
    budgets := b.db.GetActiveBudgets(ctx)
    
    for _, budget := range budgets {
        currentSpend := b.calculateCurrentSpend(budget)
        percentUsed := currentSpend / budget.AmountUSD
        
        if percentUsed >= budget.AlertThreshold && !budget.AlertSent {
            b.sendAlert(budget, currentSpend, percentUsed)
            b.db.MarkAlertSent(budget.ID)
        }
        
        if percentUsed >= 1.0 {
            // Budget exceeded — optionally stop new sessions
            if budget.HardLimit {
                b.disableNewSessions(budget.OrgID)
            }
        }
    }
}
```

---

## Reporting & Export

### CSV Export

```csv
Session ID,Session Name,Created At,Uptime Hours,CPU Hours,Memory GB-Hours,Storage GB,Compute Cost,Storage Cost,Network Cost,Total Cost
sess_001,ml-training,2026-07-01 10:00,8.5,17.0,68.0,2.4,$7.92,$0.12,$0.11,$8.15
sess_002,data-pipeline,2026-07-02 14:30,12.2,12.2,48.8,5.1,$5.36,$0.18,$0.24,$5.78
...
```

### PDF Report

Monthly cost report with:
- Executive summary (total cost, % change)
- Cost breakdown (compute/storage/network)
- Top 10 spending sessions
- Recommendations
- Budget status

---

## Success Metrics

- % of orgs using cost tracking (target: 80%)
- Avg cost reduction after enabling recommendations (target: 20%)
- Budget alerts sent vs budget exceeded (earlier = better)
- User feedback on cost visibility

---

## Future Enhancements

- **Cost anomaly detection** — ML-based alerts for unusual spending
- **Multi-cloud cost comparison** — Compare AWS vs GCP vs Azure
- **Reserved instance recommendations** — Save 30-50% with commitments
- **Chargeback** — Invoice internal teams based on usage
- **Cost allocation tags** — Tag sessions with project/department for accounting

---

## References

- AWS Pricing: https://aws.amazon.com/ec2/pricing/
- GCP Pricing: https://cloud.google.com/compute/vm-instance-pricing
- Azure Pricing: https://azure.microsoft.com/en-us/pricing/
- Docker stats API: https://docs.docker.com/engine/api/v1.41/#tag/Container/operation/ContainerStats
