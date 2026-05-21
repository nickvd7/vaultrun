package docker

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
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
	// AllowedHosts is an optional list of hostnames or IPs the container may
	// reach when NetworkEnabled is true.
	//
	// When non-empty, a dedicated per-session Docker bridge network is created
	// and host-side iptables rules enforce that only DNS and the resolved IPs
	// of the listed hosts are reachable. AllowedHosts entries are also injected
	// into /etc/hosts via ExtraHosts for in-container DNS resolution.
	//
	// When empty (and NetworkEnabled), the container uses the default bridge
	// without iptables filtering (operator is responsible for egress policy).
	AllowedHosts []string
}

const labelNetID = "vaultrun.netid" // Docker container label that records the session network ID

// CreateSandbox creates and starts an isolated Docker container.
//
// Security steps (in order):
//  1. Cosign image signature verification (if COSIGN_PUBLIC_KEY is set)
//  2. Image pull if not cached
//  3. Per-session Docker bridge network creation + iptables egress rules
//     (only when NetworkEnabled && len(AllowedHosts) > 0)
//  4. Container create + start with seccomp, CapDrop ALL, no-new-privileges
func (c *Client) CreateSandbox(ctx context.Context, cfg SandboxConfig) (string, error) {
	// ── Step 1: image signature verification ────────────────────────────────
	if err := c.VerifyImage(ctx, cfg.Image); err != nil {
		metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
		return "", fmt.Errorf("image verification: %w", err)
	}

	// ── Step 2: ensure image is locally available ────────────────────────────
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

	// ── Step 3: network setup ────────────────────────────────────────────────
	//
	// Three modes:
	//  a. NetworkEnabled=false  → NetworkMode "none"  (no network at all)
	//  b. NetworkEnabled=true, AllowedHosts empty → NetworkMode "bridge"
	//     (default bridge, no extra iptables rules)
	//  c. NetworkEnabled=true, AllowedHosts set   → dedicated bridge network
	//     with iptables egress rules applied after container start
	//
	networkMode := container.NetworkMode("none")
	var sessionNetID string        // set when a dedicated bridge network was created
	var sessionNetName string      // used in NetworkingConfig endpoint key
	var networkingCfg *network.NetworkingConfig

	if cfg.NetworkEnabled {
		if len(cfg.AllowedHosts) > 0 {
			// Create a per-session bridge network for iptables-enforced egress.
			// Shorten the session UUID so the network name stays reasonable.
			sessionNetName = "vaultrun-" + cfg.SessionID.String()[:8]
			netResp, err := c.inner.NetworkCreate(ctx, sessionNetName, network.CreateOptions{
				Driver: "bridge",
				Labels: map[string]string{
					"vaultrun.session": cfg.SessionID.String(),
					"vaultrun.managed": "true",
				},
			})
			if err != nil {
				metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
				return "", fmt.Errorf("create session network: %w", err)
			}
			sessionNetID = netResp.ID
			networkMode = container.NetworkMode(sessionNetName)
			networkingCfg = &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{
					sessionNetName: {},
				},
			}
		} else {
			networkMode = container.NetworkMode("bridge")
		}
	}

	// ── Step 4: build container config ──────────────────────────────────────

	// Convert fractional CPUs to nano-CPUs (1 CPU = 1e9 nano-CPUs)
	nanoCPUs := int64(cfg.CPULimit * 1e9)
	memoryBytes := int64(cfg.MemoryLimitMB) * 1024 * 1024

	// Always enforce no-new-privileges; append the seccomp profile when one is
	// configured on the client (c.seccompJSON).
	securityOpt := []string{"no-new-privileges"}
	switch c.seccompJSON {
	case "":
		// Rely on daemon default — only reached when DOCKER_SECCOMP_PROFILE="default"
		// and the daemon has no built-in filter configured.
	case "default":
		securityOpt = append(securityOpt, "seccomp=default")
	default:
		// Embed the profile JSON directly in SecurityOpt.
		securityOpt = append(securityOpt, "seccomp="+c.seccompJSON)
	}

	// Resolve AllowedHosts to /etc/hosts entries for in-container DNS.
	extraHosts := resolveExtraHosts(cfg.AllowedHosts)

	containerLabels := map[string]string{
		"vaultrun.session": cfg.SessionID.String(),
		"vaultrun.managed": "true",
	}
	if sessionNetID != "" {
		containerLabels[labelNetID] = sessionNetID
	}

	hostCfg := &container.HostConfig{
		NetworkMode: networkMode,
		Mounts: []mount.Mount{
			// Primary workspace — writable bind mount at /workspace.
			{
				Type:   mount.TypeBind,
				Source: cfg.WorkspacePath,
				Target: "/workspace",
				BindOptions: &mount.BindOptions{
					Propagation: mount.PropagationRPrivate,
				},
			},
			// /tmp tmpfs — needed because HOME=/tmp and some tools write
			// temp files there. The root filesystem is read-only, so without
			// this mount any write to /tmp would fail. Size-limited to 64 MB
			// to prevent tmpfs from consuming excessive host memory.
			{
				Type:   mount.TypeTmpfs,
				Target: "/tmp",
				TmpfsOptions: &mount.TmpfsOptions{
					SizeBytes: 64 * 1024 * 1024, // 64 MB
					Mode:      0o1777,            // sticky world-rwx, matching typical /tmp perms
				},
			},
		},
		Resources: container.Resources{
			NanoCPUs: nanoCPUs,
			Memory:   memoryBytes,
			// MemorySwap == Memory disables swap, preventing containers from
			// exceeding their memory cap through the swap subsystem.
			MemorySwap: memoryBytes,
			// PidsLimit caps total process count to block fork-bomb attacks.
			PidsLimit: int64Ptr(512),
		},
		// ReadonlyRootfs prevents any process inside the container from writing
		// to the container image layers. Writes must go to /workspace (bind mount)
		// or /tmp (tmpfs). This stops malicious code from modifying shared image
		// state and makes the sandbox more deterministic across runs.
		ReadonlyRootfs: true,
		SecurityOpt:    securityOpt,
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{},
		ExtraHosts:     extraHosts,
	}

	resp, err := c.inner.ContainerCreate(ctx,
		&container.Config{
			Image:      cfg.Image,
			Cmd:        []string{"sleep", "infinity"},
			WorkingDir: "/workspace",
			User:       "nobody",
			Env:        []string{"HOME=/tmp"},
			Labels:     containerLabels,
		},
		hostCfg,
		networkingCfg,
		nil,
		cfg.ContainerName,
	)
	if err != nil {
		if sessionNetID != "" {
			_ = c.inner.NetworkRemove(ctx, sessionNetID)
		}
		metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
		return "", fmt.Errorf("container create: %w", err)
	}

	if err := c.inner.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up the created (but not started) container and any network.
		_ = c.inner.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		if sessionNetID != "" {
			_ = c.inner.NetworkRemove(ctx, sessionNetID)
		}
		metrics.ContainerCreationsTotal.WithLabelValues("failed").Inc()
		return "", fmt.Errorf("container start: %w", err)
	}

	// ── Step 5: apply iptables egress rules (after container starts) ─────────
	//
	// The bridge interface br-<networkID[:12]> is present once the network
	// exists, but we apply rules after start to ensure the container's network
	// stack is fully initialised before conntrack entries can be created.
	if sessionNetID != "" && len(cfg.AllowedHosts) > 0 {
		allowedIPs := resolveToIPs(cfg.AllowedHosts)
		chain := chainName(resp.ID)
		iface := bridgeIface(sessionNetID)
		if err := applyEgressPolicy(chain, iface, allowedIPs); err != nil {
			// Log a prominent warning but do NOT tear down the container — a
			// warning is preferable to an outage. Operators who require strict
			// enforcement should alert on this log line.
			slog.Error("sandbox: egress iptables policy failed — container running WITHOUT network filtering",
				"container_id", resp.ID,
				"session_id", cfg.SessionID,
				"err", err,
			)
		}
	}

	metrics.ContainerCreationsTotal.WithLabelValues("created").Inc()
	return resp.ID, nil
}

// StopSandbox stops and removes the container, then cleans up any associated
// per-session network and iptables egress rules.
func (c *Client) StopSandbox(ctx context.Context, containerID string) error {
	// Inspect the container first to read the vaultrun.netid label before it
	// is removed — we need it to clean up the network and iptables rules.
	var sessionNetID string
	if info, err := c.inner.ContainerInspect(ctx, containerID); err == nil {
		if info.Config != nil {
			sessionNetID = info.Config.Labels[labelNetID]
		}
	}

	timeout := 10
	if err := c.inner.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		// If it's already stopped/removed, that's fine.
		slog.Debug("sandbox: container stop (may already be stopped)", "container_id", containerID, "err", err)
	}
	metrics.ContainerStopsTotal.Inc()

	if err := c.inner.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true, RemoveVolumes: false}); err != nil {
		slog.Warn("sandbox: container remove failed", "container_id", containerID, "err", err)
	}

	// Clean up iptables rules and the dedicated Docker network, if any.
	if sessionNetID != "" {
		chain := chainName(containerID)
		iface := bridgeIface(sessionNetID)
		removeEgressPolicy(chain, iface)
		if err := c.inner.NetworkRemove(ctx, sessionNetID); err != nil {
			slog.Warn("sandbox: remove session network", "network_id", sessionNetID, "err", err)
		}
	}

	return nil
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
		if ip := net.ParseIP(h); ip != nil {
			extra = append(extra, h+":"+ip.String())
			continue
		}
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

// resolveToIPs resolves each entry in hosts to a list of IP address strings.
// Used to build the iptables allowlist. Entries that cannot be resolved are
// skipped with a warning.
func resolveToIPs(hosts []string) []string {
	var ips []string
	seen := make(map[string]bool)
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			if !seen[ip.String()] {
				ips = append(ips, ip.String())
				seen[ip.String()] = true
			}
			continue
		}
		addrs, err := net.LookupHost(h)
		if err != nil {
			slog.Warn("sandbox: resolveToIPs: failed to resolve host, skipping iptables rule",
				"host", h, "err", err)
			continue
		}
		for _, addr := range addrs {
			if !seen[addr] {
				ips = append(ips, addr)
				seen[addr] = true
			}
		}
	}
	return ips
}
