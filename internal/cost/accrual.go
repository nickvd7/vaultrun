package cost

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MinAccrualSpan is how much running time must have built up before a still
// running session is billed. Cost metrics are immutable rows, so billing every
// sweep would write one row per session per minute and cost values would round
// to zero at the column's four decimals. A session that has stopped is always
// billed for its remainder, whatever its length.
const MinAccrualSpan = 5 * time.Minute

// accrualSession is the slice of a session the accrual sweep needs.
type accrualSession struct {
	ID            uuid.UUID  `db:"id"`
	CPULimit      float64    `db:"cpu_limit"`
	MemoryLimitMB int        `db:"memory_limit_mb"`
	Since         time.Time  `db:"since"`
	StoppedAt     *time.Time `db:"stopped_at"`
}

// accrualPeriod is one interval to bill.
type accrualPeriod struct {
	Start, End time.Time
}

// planAccrual splits the time a session has not yet been billed for into
// periods that never cross a calendar month. Costs are reported per month by
// period start, so a period spanning a boundary would land entirely in one
// month. It returns nil when nothing is due yet.
func planAccrual(s accrualSession, now time.Time) []accrualPeriod {
	end := now.UTC()
	stopped := s.StoppedAt != nil
	if stopped && s.StoppedAt.Before(end) {
		end = s.StoppedAt.UTC()
	}
	start := s.Since.UTC()
	if !end.After(start) {
		return nil
	}
	if !stopped && end.Sub(start) < MinAccrualSpan {
		return nil
	}

	var periods []accrualPeriod
	for start.Before(end) {
		next := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		stop := end
		if next.Before(stop) {
			stop = next
		}
		periods = append(periods, accrualPeriod{Start: start, End: stop})
		start = stop
	}
	return periods
}

// usageFor converts a session's allocation over a period into a metric.
//
// Cost is based on the resources the session reserved (CPU and memory limits)
// for as long as it ran, not on measured use: the sandbox holds that capacity
// whether or not a command is executing. Storage and network are not metered
// yet and stay at zero.
func usageFor(s accrualSession, p accrualPeriod) *CostMetric {
	hours := p.End.Sub(p.Start).Hours()
	return &CostMetric{
		ID:            uuid.New(),
		SessionID:     s.ID,
		PeriodStart:   p.Start,
		PeriodEnd:     p.End,
		CPUCoreHours:  s.CPULimit * hours,
		MemoryGBHours: float64(s.MemoryLimitMB) / 1024.0 * hours,
		CreatedAt:     time.Now().UTC(),
	}
}

// AccrueUsage bills every session for the time that has passed since it was
// last billed, and returns how many metrics were recorded. It is safe to call
// repeatedly and from several API instances: a period that was already billed
// is skipped.
func (t *Tracker) AccrueUsage(ctx context.Context, now time.Time) (int, error) {
	var sessions []accrualSession
	err := t.db.SelectContext(ctx, &sessions, `
		SELECT s.id, s.cpu_limit, s.memory_limit_mb, s.stopped_at,
		       COALESCE(m.last_end, s.created_at) AS since
		FROM sessions s
		LEFT JOIN (
			SELECT session_id, MAX(period_end) AS last_end
			FROM cost_metrics
			GROUP BY session_id
		) m ON m.session_id = s.id
		WHERE s.stopped_at IS NULL
		   OR s.stopped_at > COALESCE(m.last_end, s.created_at)
	`)
	if err != nil {
		return 0, fmt.Errorf("list sessions to bill: %w", err)
	}

	recorded := 0
	for _, s := range sessions {
		for _, p := range planAccrual(s, now) {
			if err := ctx.Err(); err != nil {
				return recorded, err
			}
			if err := t.RecordMetric(ctx, usageFor(s, p)); err != nil {
				if isPeriodConflict(err) {
					break
				}
				slog.Error("cost accrual failed", "session_id", s.ID, "error", err)
				break
			}
			recorded++
		}
	}
	return recorded, nil
}

// isPeriodConflict reports whether err is the unique violation raised when
// another instance already billed the same period.
func isPeriodConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cost_metrics_period_unique")
}

// StartAccrual bills sessions every interval until ctx is cancelled.
func (t *Tracker) StartAccrual(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	slog.Info("cost accrual started", "interval", interval, "min_span", MinAccrualSpan)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := t.AccrueUsage(ctx, time.Now())
			if err != nil {
				slog.Error("cost accrual sweep failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Debug("cost accrual recorded metrics", "count", n)
			}
		}
	}
}
