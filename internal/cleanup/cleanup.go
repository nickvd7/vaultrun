// Package cleanup provides a background goroutine that stops containers for
// sessions that have been idle (no completed runs) beyond the configured timeout.
package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/models"
)

// Start launches the idle-session cleanup loop. It runs until ctx is cancelled.
// interval controls how frequently the sweep runs; idleFor is how long a session
// must have been quiet before its container is stopped (workspace is preserved).
func Start(ctx context.Context, db *sqlx.DB, docker *dockerpkg.Client, interval, idleFor time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if idleFor <= 0 {
		idleFor = 30 * time.Minute
	}

	slog.Info("idle cleanup started", "interval", interval, "idle_threshold", idleFor)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(ctx, db, docker, idleFor)
		}
	}
}

func sweep(ctx context.Context, db *sqlx.DB, docker *dockerpkg.Client, idleFor time.Duration) {
	sessions, err := dbpkg.ListActiveSessions(ctx, db)
	if err != nil {
		slog.Error("cleanup: list active sessions", "err", err)
		return
	}

	cutoff := time.Now().UTC().Add(-idleFor)

	for _, s := range sessions {
		if shouldStop(s, cutoff) {
			stopSession(ctx, db, docker, s)
		}
	}
}

// shouldStop returns true if the session has had no activity since cutoff.
func shouldStop(s *models.Session, cutoff time.Time) bool {
	// Use UpdatedAt as a proxy for last activity
	return s.UpdatedAt.Before(cutoff)
}

func stopSession(ctx context.Context, db *sqlx.DB, docker *dockerpkg.Client, s *models.Session) {
	slog.Info("cleanup: stopping idle session",
		"session_id", s.ID,
		"last_activity", s.UpdatedAt,
	)

	if s.ContainerID != nil {
		if err := docker.StopSandbox(ctx, *s.ContainerID); err != nil {
			slog.Warn("cleanup: stop container", "session_id", s.ID, "err", err)
		}
	}

	if err := dbpkg.StopSession(ctx, db, s.ID); err != nil {
		slog.Error("cleanup: mark session stopped", "session_id", s.ID, "err", err)
	}
}
