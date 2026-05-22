// Package warmpool maintains a pool of pre-started Docker containers for a
// single configured image, eliminating docker pull+create latency on the
// hot path.
//
// The pool is optional. When WARM_POOL_SIZE is 0 or WARM_POOL_IMAGE is unset
// the pool is disabled and session creation uses the standard on-demand path.
package warmpool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
)

// Entry is a pre-warmed container ready for takeover by a new session.
type Entry struct {
	ContainerID   string
	WorkspacePath string // host path; already bound-mounted inside the container
}

// Pool maintains a buffered channel of warm containers and refills it in the
// background. All methods are goroutine-safe.
type Pool struct {
	dockerCli   *client.Client
	image       string
	size        int
	baseDir     string // workspace base; each warm container gets its own sub-dir
	seccompJSON string // raw JSON profile, "default", or ""

	pool   chan Entry
	stopCh chan struct{}
	once   sync.Once
}

// New creates a warm pool. Call Start to begin filling it.
func New(dockerCli *client.Client, image string, size int, baseDir, seccompJSON string) *Pool {
	return &Pool{
		dockerCli:   dockerCli,
		image:       image,
		size:        size,
		baseDir:     baseDir,
		seccompJSON: seccompJSON,
		pool:        make(chan Entry, size),
		stopCh:      make(chan struct{}),
	}
}

// Image returns the image this pool pre-warms.
func (p *Pool) Image() string { return p.image }

// Start launches the background fill goroutine.
func (p *Pool) Start(ctx context.Context) {
	go p.fill(ctx)
}

// Acquire pops a warm container (non-blocking). Returns (entry, true) when one
// is available, or (Entry{}, false) when the pool is empty (caller falls back
// to on-demand creation).
func (p *Pool) Acquire() (Entry, bool) {
	select {
	case e := <-p.pool:
		return e, true
	default:
		return Entry{}, false
	}
}

// Stop drains and removes all warm containers. Safe to call multiple times.
func (p *Pool) Stop() {
	p.once.Do(func() {
		close(p.stopCh)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			select {
			case e := <-p.pool:
				if err := p.dockerCli.ContainerRemove(ctx, e.ContainerID,
					container.RemoveOptions{Force: true}); err != nil {
					slog.Warn("warmpool: remove on stop", "id", e.ContainerID, "err", err)
				}
				_ = os.RemoveAll(e.WorkspacePath)
			default:
				return
			}
		}
	})
}

// fill keeps the pool at capacity by creating containers in the background.
func (p *Pool) fill(ctx context.Context) {
	for {
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		for len(p.pool) < p.size {
			select {
			case <-p.stopCh:
				return
			case <-ctx.Done():
				return
			default:
			}
			e, err := p.createOne(ctx)
			if err != nil {
				slog.Warn("warmpool: create failed", "image", p.image, "err", err)
				select {
				case <-time.After(15 * time.Second):
				case <-p.stopCh:
					return
				}
				break // restart outer loop
			}
			select {
			case p.pool <- e:
				slog.Debug("warmpool: ready", "image", p.image, "id", e.ContainerID[:12], "depth", len(p.pool))
			case <-p.stopCh:
				rmCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = p.dockerCli.ContainerRemove(rmCtx, e.ContainerID, container.RemoveOptions{Force: true})
				_ = os.RemoveAll(e.WorkspacePath)
				cancel()
				return
			}
		}

		select {
		case <-time.After(5 * time.Second):
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// createOne starts a single warm container with a fresh temporary workspace.
func (p *Pool) createOne(ctx context.Context) (Entry, error) {
	wsID := uuid.New()
	wsPath := fmt.Sprintf("%s/warm-%s", p.baseDir, wsID.String())
	if err := os.MkdirAll(wsPath, 0o777); err != nil {
		return Entry{}, fmt.Errorf("mkdir workspace: %w", err)
	}
	if err := os.Chmod(wsPath, 0o777); err != nil {
		return Entry{}, fmt.Errorf("chmod workspace: %w", err)
	}

	secOpt := []string{"no-new-privileges"}
	if p.seccompJSON != "" {
		secOpt = append(secOpt, "seccomp="+p.seccompJSON)
	}

	hostCfg := &container.HostConfig{
		NetworkMode: "none",
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: wsPath,
				Target: "/workspace",
				BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate},
			},
			{
				Type:   mount.TypeTmpfs,
				Target: "/tmp",
				TmpfsOptions: &mount.TmpfsOptions{SizeBytes: 64 * 1024 * 1024, Mode: 0o1777},
			},
		},
		Resources: container.Resources{
			NanoCPUs:   1_000_000_000, // 1 vCPU default
			Memory:     512 * 1024 * 1024,
			MemorySwap: 512 * 1024 * 1024,
			PidsLimit:  int64Ptr(512),
		},
		ReadonlyRootfs: true,
		SecurityOpt:    secOpt,
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{},
	}

	name := fmt.Sprintf("vaultrun-warm-%s", wsID.String()[:8])
	resp, err := p.dockerCli.ContainerCreate(ctx,
		&container.Config{
			Image:      p.image,
			Cmd:        []string{"sleep", "infinity"},
			WorkingDir: "/workspace",
			User:       "nobody",
			Env:        []string{"HOME=/tmp"},
		},
		hostCfg, nil, nil, name,
	)
	if err != nil {
		_ = os.RemoveAll(wsPath)
		return Entry{}, fmt.Errorf("container create: %w", err)
	}

	if err := p.dockerCli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = p.dockerCli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = os.RemoveAll(wsPath)
		return Entry{}, fmt.Errorf("container start: %w", err)
	}

	return Entry{ContainerID: resp.ID, WorkspacePath: wsPath}, nil
}

func int64Ptr(i int64) *int64 { return &i }
