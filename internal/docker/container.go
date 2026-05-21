package docker

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/metrics"
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
	// AllowedHosts is an optional list of hostnames or IPs that the container
	// may reach when NetworkEnabled is true. Entries are resolved to IP
	// addresses at creation time and injected into /etc/hosts via ExtraHosts.
	// Note: this provides DNS-level resolution hints only. For full network
	// isolation, operators should apply host-side iptables rules in addition.
	AllowedHosts []string
}

// CreateSandbox creates and starts an isolated Docker container.
func (c *Client) CreateSandbox(ctx context.Context, cfg SandboxConfig) (string, error) {
	// Ensure image is available
	exists, err := c.ImageExists(ctx, cfg.Image)
	if err != nil {
		metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
		return "", fmt.Errorf("check image: %w", err)
	}
	if !exists {
		if err := c.PullImage(ctx, cfg.Image); err != nil {
			metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
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

	// Build SecurityOpt list. Always enforce no-new-privileges; append the
	// seccomp profile when one is configured on the client (c.seccompJSON).
	securityOpt := []string{"no-new-privileges"}
	switch c.seccompJSON {
	case "":
		// Rely on daemon default — already applies Docker's built-in filter.
	case "default":
		securityOpt = append(securityOpt, "seccomp=default")
	default:
		// Embed the custom JSON profile directly into SecurityOpt.
		securityOpt = append(securityOpt, "seccomp="+c.seccompJSON)
	}

	// Resolve AllowedHosts to /etc/hosts entries ("hostname:ip").
	// This gives the container correct DNS resolution for the allowed set.
	// Real network egress filtering requires host-side iptables; ExtraHosts
	// alone does not block other outbound connections.
	extraHosts := resolveExtraHosts(cfg.AllowedHosts)

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
			// MemorySwap = Memory disables swap, preventing containers from
			// exceeding their memory cap through the swap subsystem (L-4).
			MemorySwap: memoryBytes,
			// PidsLimit caps total process count to block fork-bomb attacks
			// from exhausting the host PID namespace (L-5).
			PidsLimit: int64Ptr(512),
		},
		// Security hardening: no-new-privileges + optional seccomp profile.
		// CapDrop ALL ensures the container starts with zero Linux capabilities.
		ReadonlyRootfs: false, // workspace is writable via bind mount
		SecurityOpt:    securityOpt,
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{}, // grant nothing extra
		ExtraHosts:     extraHosts,
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
		metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
		return "", fmt.Errorf("container create: %w", err)
	}

	if err := c.inner.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up the created (but not started) container
		_ = c.inner.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
		return "", fmt.Errorf("container start: %w", err)
	}

	metrics.ContainerCreationsTotal.WithLabelValues("created").Inc()
	return resp.ID, nil
}

// StopSandbox stops and removes the container.
func (c *Client) StopSandbox(ctx context.Context, containerID string) error {
	timeout := 10
	if err := c.inner.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		// If it's already stopped/removed, that's fine
		return nil
	}
	metrics.ContainerStopsTotal.Inc()
	return c.inner.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: false})
}

func int64Ptr(v int64) *int64 { return &v }

// ContainerRunning returns true if the container exists and is running.
func (c *Client) ContainerRunning(ctx context.Context, containerID string) (bool, error) {
	info, err := c.inner.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, nil // not found == not running
	}
	return info.State.Running, nil
}

// resolveExtraHosts converts a list of hostnames/IPs into Docker ExtraHosts
// entries of the form "hostname:ip". Pure IP addresses are added as "ip:ip".
// Hostnames that fail to resolve are skipped with a warning.
func resolveExtraHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return nil
	}
	var extra []string
	for _, h := range hosts {
		// If it's already a raw IP, add "ip:ip" so /etc/hosts maps it to itself.
		if ip := net.ParseIP(h); ip != nil {
			extra = append(extra, h+":"+ip.String())
			continue
		}
		// Resolve hostname → IPs.
		addrs, err := net.LookupHost(h)
		if err != nil {
			slog.Warn("sandbox: failed to resolve allowed host, skipping",
				"host", h, "err", err)
			continue
		}
		for _, addr := range addrs {
			extra = append(extra, h+":"+addr)
		}
	}
	return extra
}
