// Package quota keeps sandboxes within their workspace size cap
// (MAX_WORKSPACE_MB).
//
// The workspace is a bind mount on the host's disk, so the cap cannot be set
// on the container. Uploads through the API are checked when they arrive; this
// package covers what the sandbox writes itself: a watcher measures every
// running session's workspace and pauses (freezes) the container of one that
// went over, which stops every process in it, background ones included. Runs
// are refused while the workspace is over the cap; once the user has deleted
// files through the API, the next run unpauses the container.
//
// A watcher bounds the overshoot to what a sandbox can write between two
// measurements. A hard limit needs filesystem quotas (XFS project quotas) on
// the workspace directory; see docs/production-hardening.md.
package quota

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

// ErrOverQuota means the workspace is larger than the cap.
var ErrOverQuota = errors.New("workspace over quota")

// Enforcer pauses sandboxes whose workspace is over MaxBytes.
type Enforcer struct {
	DB       *sqlx.DB
	Docker   *dockerpkg.Client
	WS       *workspace.Manager
	Audit    *audit.Logger
	MaxBytes int64
}

// Size is the total size of the regular files in a workspace. It walks
// through the workspace's os.Root, so symlinks are not followed out of it.
func Size(ws *workspace.Manager, sessionID uuid.UUID) (int64, error) {
	root, err := ws.Root(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer root.Close()
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // removed meanwhile
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// Admit is called before a run. It returns an error wrapping ErrOverQuota when
// the workspace is over the cap (pausing the container if it is not already),
// and unpauses a container that was paused for the quota once it is back
// under.
func (e *Enforcer) Admit(ctx context.Context, s *models.Session) error {
	if e == nil || e.MaxBytes <= 0 || s.ContainerID == nil {
		return nil
	}
	size, err := Size(e.WS, s.ID)
	if err != nil {
		return nil // measuring failed; do not block work on it
	}
	paused := e.paused(ctx, *s.ContainerID)
	if size > e.MaxBytes {
		if !paused {
			e.pause(ctx, s, size)
		}
		return fmt.Errorf("%w: the workspace holds %d MB, the limit is %d MB; delete files (DELETE /api/v1/sessions/%s/files/...) and run again",
			ErrOverQuota, size>>20, e.MaxBytes>>20, s.ID)
	}
	if paused {
		if err := e.Docker.Inner().ContainerUnpause(ctx, *s.ContainerID); err != nil {
			return fmt.Errorf("unpause sandbox: %w", err)
		}
		slog.Info("quota: sandbox back under its workspace cap, unpaused", "session_id", s.ID, "bytes", size)
	}
	return nil
}

// Run measures every running session's workspace each interval and pauses
// the ones over the cap, until ctx ends.
func (e *Enforcer) Run(ctx context.Context, interval time.Duration) {
	if e.MaxBytes <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.sweep(ctx)
		}
	}
}

func (e *Enforcer) sweep(ctx context.Context) {
	sessions, err := dbpkg.ListActiveSessions(ctx, e.DB)
	if err != nil {
		slog.Warn("quota: list sessions", "err", err)
		return
	}
	for _, s := range sessions {
		if s.ContainerID == nil {
			continue
		}
		size, err := Size(e.WS, s.ID)
		if err != nil || size <= e.MaxBytes {
			continue
		}
		if !e.paused(ctx, *s.ContainerID) {
			e.pause(ctx, s, size)
		}
	}
}

func (e *Enforcer) paused(ctx context.Context, containerID string) bool {
	info, err := e.Docker.Inner().ContainerInspect(ctx, containerID)
	return err == nil && info.State != nil && info.State.Paused
}

func (e *Enforcer) pause(ctx context.Context, s *models.Session, size int64) {
	if err := e.Docker.Inner().ContainerPause(ctx, *s.ContainerID); err != nil {
		slog.Warn("quota: pause sandbox over its workspace cap", "session_id", s.ID, "err", err)
		return
	}
	slog.Warn("quota: workspace over its cap, sandbox paused", "session_id", s.ID, "bytes", size, "max_bytes", e.MaxBytes)
	id := s.ID
	e.Audit.Log(ctx, audit.Event{
		Actor:     "system",
		SessionID: &id,
		Action:    "session.quota_exceeded",
		Metadata:  models.JSONB{"workspace_bytes": size, "max_bytes": e.MaxBytes},
	})
}
