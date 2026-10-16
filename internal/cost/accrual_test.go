package cost

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func ts(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func TestPlanAccrualRunningSessionWaitsForMinSpan(t *testing.T) {
	s := accrualSession{ID: uuid.New(), Since: ts("2026-10-15T10:00:00Z")}

	if got := planAccrual(s, ts("2026-10-15T10:04:00Z")); got != nil {
		t.Fatalf("billed %v before MinAccrualSpan", got)
	}
	got := planAccrual(s, ts("2026-10-15T10:06:00Z"))
	if len(got) != 1 || !got[0].End.Equal(ts("2026-10-15T10:06:00Z")) {
		t.Fatalf("got %v, want one period ending at now", got)
	}
}

func TestPlanAccrualStoppedSessionBillsRemainderUpToStop(t *testing.T) {
	stop := ts("2026-10-15T10:01:00Z")
	s := accrualSession{Since: ts("2026-10-15T10:00:00Z"), StoppedAt: &stop}

	got := planAccrual(s, ts("2026-10-15T12:00:00Z"))
	if len(got) != 1 || !got[0].End.Equal(stop) {
		t.Fatalf("got %v, want one period ending at stopped_at", got)
	}
	if got := planAccrual(accrualSession{Since: stop, StoppedAt: &stop}, ts("2026-10-15T12:00:00Z")); got != nil {
		t.Fatalf("fully billed session produced %v", got)
	}
}

func TestPlanAccrualSplitsAtMonthBoundary(t *testing.T) {
	s := accrualSession{Since: ts("2026-10-31T23:00:00Z")}

	got := planAccrual(s, ts("2026-12-01T01:00:00Z"))
	if len(got) != 3 {
		t.Fatalf("got %d periods, want 3: %v", len(got), got)
	}
	if !got[0].End.Equal(ts("2026-11-01T00:00:00Z")) || !got[1].Start.Equal(got[0].End) ||
		!got[1].End.Equal(ts("2026-12-01T00:00:00Z")) || !got[2].End.Equal(ts("2026-12-01T01:00:00Z")) {
		t.Fatalf("unexpected periods: %v", got)
	}
}

func TestUsageForBillsAllocatedResources(t *testing.T) {
	s := accrualSession{ID: uuid.New(), CPULimit: 2, MemoryLimitMB: 512}
	m := usageFor(s, accrualPeriod{Start: ts("2026-10-15T10:00:00Z"), End: ts("2026-10-15T12:00:00Z")})

	if m.CPUCoreHours != 4 {
		t.Errorf("cpu core hours = %v, want 4", m.CPUCoreHours)
	}
	if m.MemoryGBHours != 1 {
		t.Errorf("memory gb hours = %v, want 1", m.MemoryGBHours)
	}
}

func TestRoundTo(t *testing.T) {
	if got := roundTo(0.00066666666, costDecimals); got != 0.0007 {
		t.Errorf("roundTo = %v, want 0.0007", got)
	}
}
