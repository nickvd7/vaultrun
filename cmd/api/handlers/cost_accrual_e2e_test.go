//go:build integration

package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/cost"
)

// TestAccrueUsageBillsSessionsOnce: RecordMetric used to have no caller, so
// Costs stayed at zero however long sessions ran.
func TestAccrueUsageBillsSessionsOnce(t *testing.T) {
	truncateFeatureTables(t)
	ctx := context.Background()
	tracker := cost.New(testDB, []byte(testHMACKey))

	now := time.Now().UTC().Truncate(time.Microsecond)
	stopped := seedFeatureSession(t, "alice", nil, false)
	running := seedFeatureSession(t, "alice", nil, false)
	fresh := seedFeatureSession(t, "alice", nil, false)

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := testDB.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`UPDATE sessions SET cpu_limit = 2, memory_limit_mb = 1024, created_at = $2, stopped_at = $3 WHERE id = $1`,
		stopped, now.Add(-2*time.Hour), now.Add(-time.Hour))
	mustExec(`UPDATE sessions SET cpu_limit = 1, memory_limit_mb = 512, created_at = $2 WHERE id = $1`,
		running, now.Add(-30*time.Minute))
	mustExec(`UPDATE sessions SET created_at = $2 WHERE id = $1`, fresh, now.Add(-time.Minute))

	n, err := tracker.AccrueUsage(ctx, now)
	if err != nil {
		t.Fatalf("accrue: %v", err)
	}
	if n < 2 {
		t.Fatalf("recorded %d metrics, want one for the stopped and one for the running session", n)
	}

	metrics, err := tracker.GetSessionCosts(ctx, stopped)
	if err != nil || len(metrics) == 0 {
		t.Fatalf("stopped session metrics = %v, %v", metrics, err)
	}
	var cpu float64
	for i := range metrics {
		cpu += metrics[i].CPUCoreHours
		if !tracker.VerifyMetric(&metrics[i]) {
			t.Errorf("metric %d does not verify after a database round trip", i)
		}
	}
	if cpu < 1.99 || cpu > 2.01 {
		t.Errorf("stopped session billed %.4f core-hours, want 2 (2 cores for 1h)", cpu)
	}

	if m, _ := tracker.GetSessionCosts(ctx, fresh); len(m) != 0 {
		t.Errorf("session running for one minute was billed %d times, want 0 until MinAccrualSpan", len(m))
	}

	again, err := tracker.AccrueUsage(ctx, now)
	if err != nil || again != 0 {
		t.Errorf("second sweep recorded %d metrics (err %v), want 0", again, err)
	}

	var total float64
	if err := testDB.Get(&total, `SELECT total_cost FROM sessions WHERE id = $1`, uuid.UUID(stopped)); err != nil || total <= 0 {
		t.Errorf("session total_cost = %v (err %v), want > 0", total, err)
	}
}

// The Costs page listed every session that ever recorded a metric, including
// ones whose cost rounds to nothing, and did not say which had ended.
func TestCostBreakdownMarksEndedSessionsAndSkipsZeroCost(t *testing.T) {
	truncateFeatureTables(t)
	ctx := context.Background()
	tracker := cost.New(testDB, []byte(testHMACKey))

	now := time.Now().UTC().Truncate(time.Microsecond)
	ended := seedFeatureSession(t, "alice", nil, false)
	live := seedFeatureSession(t, "alice", nil, false)
	blink := seedFeatureSession(t, "alice", nil, false)
	testDB.MustExec(`UPDATE sessions SET cpu_limit = 2, memory_limit_mb = 1024, created_at = $2, stopped_at = $3 WHERE id = $1`,
		ended, now.Add(-2*time.Hour), now.Add(-time.Hour))
	testDB.MustExec(`UPDATE sessions SET cpu_limit = 1, memory_limit_mb = 512, created_at = $2 WHERE id = $1`,
		live, now.Add(-30*time.Minute))
	testDB.MustExec(`UPDATE sessions SET cpu_limit = 0.1, memory_limit_mb = 64, created_at = $2, stopped_at = $3 WHERE id = $1`,
		blink, now.Add(-time.Hour), now.Add(-time.Hour+time.Second))

	if _, err := tracker.AccrueUsage(ctx, now); err != nil {
		t.Fatal(err)
	}
	b, err := tracker.GetCostBreakdown(ctx, now.Format("2006-01"), cost.DeploymentScope())
	if err != nil {
		t.Fatal(err)
	}
	var sawEnded, sawLive bool
	for _, s := range b.TopSessions {
		switch s.SessionID {
		case ended:
			sawEnded = true
			if s.StoppedAt == nil {
				t.Error("ended session is not marked as stopped")
			}
		case live:
			sawLive = true
			if s.StoppedAt != nil {
				t.Error("running session is marked as stopped")
			}
		case blink:
			t.Errorf("session costing %.4f is listed", s.TotalCost)
		}
		if s.TotalCost <= 0 {
			t.Errorf("zero-cost session %s listed", s.SessionID)
		}
	}
	if !sawEnded || !sawLive {
		t.Errorf("ended=%v live=%v in %+v", sawEnded, sawLive, b.TopSessions)
	}
}
