// Package cleanup provides background goroutines that:
//  1. Stop containers for sessions idle beyond the configured timeout.
//  2. Purge audit log entries older than the configured retention window.
package cleanup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/metrics"
	"github.com/nickvd7/vaultrun/internal/models"
)

// cleaner holds shared state for sweep runs.
type cleaner struct {
	db     *sqlx.DB
	docker *dockerpkg.Client
	audit  *audit.Logger
	// mu ensures only one sweep runs at a time (M-1): if a sweep is slow
	// (Docker or DB latency) we skip the next tick rather than stacking goroutines.
	mu sync.Mutex
}

// Start launches the idle-session cleanup loop. It runs until ctx is cancelled.
// interval controls how frequently the sweep runs; idleFor is how long a session
// must have been quiet before its container is stopped (workspace is preserved).
//
// When retentionDays > 0, audit logs older than that many days are also pruned
// on every sweep. Set to 0 to retain audit logs indefinitely.
func Start(ctx context.Context, db *sqlx.DB, docker *dockerpkg.Client, al *audit.Logger, interval, idleFor time.Duration, retentionDays int) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if idleFor <= 0 {
		idleFor = 30 * time.Minute
	}

	slog.Info("idle cleanup started",
		"interval", interval,
		"idle_threshold", idleFor,
		"audit_retention_days", retentionDays,
	)

	cl := &cleaner{db: db, docker: docker, audit: al}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cl.sweep(ctx, idleFor)
			if retentionDays > 0 {
				cl.pruneAuditLogs(ctx, retentionDays)
			}
		}
	}
}

func (cl *cleaner) sweep(ctx context.Context, idleFor time.Duration) {
	// Skip this tick if the previous sweep hasn't finished yet (M-1).
	if !cl.mu.TryLock() {
		slog.Warn("cleanup: sweep skipped — previous sweep still running")
		return
	}
	defer cl.mu.Unlock()

	sessions, err := dbpkg.ListActiveSessions(ctx, cl.db)
	if err != nil {
		slog.Error("cleanup: list active sessions", "err", err)
		return
	}

	cutoff := time.Now().UTC().Add(-idleFor)

	for _, s := range sessions {
		if shouldStop(s, cutoff) {
			cl.stopSession(ctx, s)
		}
	}
}

// pruneAuditLogs deletes audit log rows older than retentionDays days.
// Runs after every sweep tick when a retention window is configured.
func (cl *cleaner) pruneAuditLogs(ctx context.Context, retentionDays int) {
	before := time.Now().UTC().AddDate(0, 0, -retentionDays)
	n, err := dbpkg.DeleteOldAuditLogs(ctx, cl.db, before)
	if err != nil {
		slog.Error("cleanup: prune audit logs", "err", err)
		return
	}
	if n > 0 {
		slog.Info("cleanup: pruned old audit logs",
			"deleted", n,
			"older_than", before.Format(time.DateOnly),
		)
	}
}

// shouldStop returns true if the session has had no activity since cutoff.
func shouldStop(s *models.Session, cutoff time.Time) bool {
	return s.UpdatedAt.Before(cutoff)
}

func (cl *cleaner) stopSession(ctx context.Context, s *models.Session) {
	slog.Info("cleanup: stopping idle session",
		"session_id", s.ID,
		"last_activity", s.UpdatedAt,
	)

	if s.ContainerID != nil {
		if err := cl.docker.StopSandbox(ctx, *s.ContainerID); err != nil {
			slog.Warn("cleanup: stop container", "session_id", s.ID, "err", err)
		}
	}

	if err := dbpkg.StopSession(ctx, cl.db, s.ID); err != nil {
		slog.Error("cleanup: mark session stopped", "session_id", s.ID, "err", err)
		return
	}

	metrics.ActiveSessions.Dec()

	// Emit audit event so forensic trail is complete (H-4).
	if cl.audit != nil {
		sidCopy := s.ID
		cl.audit.Log(ctx, audit.Event{
			Actor:     "cleanup",
			SessionID: &sidCopy,
			Action:    models.ActionSessionExpired,
			Metadata:  models.JSONB{"reason": "idle_timeout"},
		})
	}
}
