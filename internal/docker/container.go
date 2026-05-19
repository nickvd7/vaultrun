package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/uuid"
)

// SandboxConfig holds the parameters for creating a session container.
type SandboxConfig struct {
	SessionID      uuid.UUID
	Image          string
	WorkspacePath  string
	NetworkEnabled bool
	CPULimit       float64 // fractional CPUs, e.g. 0.5 = half a core
	MemoryLimitMB  int
	ContainerName  string
}

// CreateSandbox creates and starts an isolated Docker container.
func (c *Client) CreateSandbox(ctx context.Context, cfg SandboxConfig) (string, error) {
	// Ensure image is available
	exists, err := c.ImageExists(ctx, cfg.Image)
	if err != nil {
		return "", fmt.Errorf("check image: %w", err)
	}
	if !exists {
		if err := c.PullImage(ctx, cfg.Image); err != nil {
			return "", err
		}
	}

	networkMode := container.NetworkMode("none")
	if cfg.NetworkEnabled {
		networkMode = container.NetworkMode("bridge")
	}

	// Convert fractional CPUs to nano-CPUs (1 CPU = 1e9 nano-CPUs)
	nanoCPUs := int64(cfg.CPULimit * 1e9)
	memoryBytes := int64(cfg.MemoryLimitMB) * 1024 * 1024

	hostCfg := &container.HostConfig{
		NetworkMode: networkMode,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: cfg.WorkspacePath,
				Target: "/workspace",
				BindOptions: &mount.BindOptions{
					Propagation: mount.PropagationRPrivate,
				},
			},
		},
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memoryBytes,
		},
		// Security hardening
		ReadonlyRootfs: false, // workspace is writable via bind mount
		SecurityOpt:    []string{"no-new-privileges"},
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{}, // grant nothing extra
	}

	resp, err := c.inner.ContainerCreate(ctx,
		&container.Config{
			Image:      cfg.Image,
			Cmd:        []string{"sleep", "infinity"},
			WorkingDir: "/workspace",
			User:       "nobody",
			Env:        []string{"HOME=/tmp"},
			Labels: map[string]string{
				"vaultrun.session": cfg.SessionID.String(),
				"vaultrun.managed": "true",
			},
		},
		hostCfg,
		nil,
		nil,
		cfg.ContainerName,
	)
	if err != nil {
		return "", fmt.Errorf("container create: %w", err)
	}

	if err := c.inner.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up the created (but not started) container
		_ = c.inner.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("container start: %w", err)
	}

	return resp.ID, nil
}

// StopSandbox stops and removes the container.
func (c *Client) StopSandbox(ctx context.Context, containerID string) error {
	timeout := 10
	if err := c.inner.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		// If it's already stopped/removed, that's fine
		return nil
	}
	return c.inner.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: false})
}

// ContainerRunning returns true if the container exists and is running.
func (c *Client) ContainerRunning(ctx context.Context, containerID string) (bool, error) {
	info, err := c.inner.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, nil // not found == not running
	}
	return info.State.Running, nil
}
