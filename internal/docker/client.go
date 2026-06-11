package docker

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// defaultSeccompProfile is the vaultrun-curated seccomp policy embedded at
// compile time. It is used whenever DOCKER_SECCOMP_PROFILE is not set, so the
// binary is self-contained and doesn't depend on the daemon's built-in profile.
//
//go:embed seccomp/profile.json
var defaultSeccompProfile string

// Client wraps the Docker SDK client with optional seccomp enforcement.
type Client struct {
	inner           *client.Client
	seccompJSON     string // raw JSON profile; "default" = daemon explicit default
	cosignPublicKey string // path to cosign public key file; empty = verification disabled
	requireTlog     bool   // COSIGN_REQUIRE_TLOG=true: pass --tlog-verify to cosign (requires Rekor)
}

// New creates a new Docker client using the environment / DOCKER_HOST.
//
// Seccomp: DOCKER_SECCOMP_PROFILE controls which profile is applied:
//   - ""        → use the embedded vaultrun seccomp profile (default, recommended)
//   - "default" → pass seccomp=default explicitly to rely on the daemon's built-in filter
//   - "/path"   → load and embed the JSON from the given file path
//
// Cosign: when COSIGN_PUBLIC_KEY points to a PEM public key, every image is
// verified with `cosign verify --key <file> <image>` before use. If the key
// is set but the cosign binary is missing, CreateSandbox fails closed.
func New() (*Client, error) {
	c, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	dc := &Client{inner: c}

	profile := os.Getenv("DOCKER_SECCOMP_PROFILE")
	switch profile {
	case "":
		// Use the vaultrun seccomp profile embedded at compile time. This
		// ensures syscall filtering is applied even when the Docker daemon's
		// built-in default is absent or looser than our requirements.
		dc.seccompJSON = defaultSeccompProfile
		slog.Info("docker: using embedded vaultrun seccomp profile")
	case "default":
		dc.seccompJSON = "default"
		slog.Info("docker: using daemon default seccomp profile (explicit)")
	default:
		data, err := os.ReadFile(profile)
		if err != nil {
			return nil, fmt.Errorf("load seccomp profile %q: %w", profile, err)
		}
		dc.seccompJSON = string(data)
		slog.Info("docker: custom seccomp profile loaded", "path", profile)
	}

	// Cosign image verification — optional but strongly recommended in production.
	dc.cosignPublicKey = os.Getenv("COSIGN_PUBLIC_KEY")
	if dc.cosignPublicKey != "" {
		dc.requireTlog = os.Getenv("COSIGN_REQUIRE_TLOG") == "true"
		slog.Info("docker: cosign image verification enabled",
			"key", dc.cosignPublicKey, "require_tlog", dc.requireTlog)
	} else {
		slog.Warn("docker: cosign image verification disabled (set COSIGN_PUBLIC_KEY to enable)")
	}

	return dc, nil
}

func (c *Client) Inner() *client.Client {
	return c.inner
}

// SeccompJSON returns the raw seccomp profile JSON configured for this client.
// Used by the warm pool so it can apply the same profile as CreateSandbox.
func (c *Client) SeccompJSON() string {
	return c.seccompJSON
}

// PullImage pulls an image if not already present.
func (c *Client) PullImage(ctx context.Context, image string) error {
	out, err := c.inner.ImagePull(ctx, image, dockerImagePullOptions())
	if err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	defer out.Close()
	// Drain the response to wait for the pull to finish.
	_, _ = io.Copy(io.Discard, out)
	return nil
}

// ImageExists returns true if the image is already locally available.
func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	_, _, err := c.inner.ImageInspectWithRaw(ctx, image)
	if client.IsErrNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ImageSummary is a trimmed image descriptor returned by ListImages.
type ImageSummary struct {
	ID        string    `json:"id"`
	Tags      []string  `json:"tags"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// ListImages returns locally available Docker images.
func (c *Client) ListImages(ctx context.Context) ([]ImageSummary, error) {
	imgs, err := c.inner.ImageList(ctx, dockerimage.ListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	out := make([]ImageSummary, 0, len(imgs))
	for _, img := range imgs {
		tags := img.RepoTags
		if len(tags) == 0 {
			tags = []string{"<none>:<none>"}
		}
		out = append(out, ImageSummary{
			ID:        img.ID,
			Tags:      tags,
			SizeBytes: img.Size,
			CreatedAt: time.Unix(img.Created, 0).UTC(),
		})
	}
	return out, nil
}

// containerStatsJSON is a minimal projection of Docker's stats payload.
type containerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage    struct{ TotalUsage uint64 `json:"total_usage"` } `json:"cpu_usage"`
		SystemUsage uint64                                           `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

// ContainerStatsResult holds the one-shot resource metrics for a container.
type ContainerStatsResult struct {
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryBytes      uint64  `json:"memory_bytes"`
	MemoryLimitBytes uint64  `json:"memory_limit_bytes"`
	NetworkRxBytes   uint64  `json:"network_rx_bytes"`
	NetworkTxBytes   uint64  `json:"network_tx_bytes"`
}

// ContainerStats returns a single-sample resource snapshot for containerName.
func (c *Client) ContainerStats(ctx context.Context, containerName string) (*ContainerStatsResult, error) {
	resp, err := c.inner.ContainerStats(ctx, containerName, false)
	if err != nil {
		return nil, fmt.Errorf("stats %s: %w", containerName, err)
	}
	defer resp.Body.Close()

	var raw containerStatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode stats: %w", err)
	}

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage - raw.PreCPUStats.SystemUsage)
	numCPUs := float64(raw.CPUStats.OnlineCPUs)
	if numCPUs == 0 {
		numCPUs = float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
	}
	var cpuPct float64
	if sysDelta > 0 && cpuDelta > 0 {
		cpuPct = (cpuDelta / sysDelta) * numCPUs * 100.0
	}

	var rxBytes, txBytes uint64
	for _, n := range raw.Networks {
		rxBytes += n.RxBytes
		txBytes += n.TxBytes
	}

	return &ContainerStatsResult{
		CPUPercent:       cpuPct,
		MemoryBytes:      raw.MemoryStats.Usage,
		MemoryLimitBytes: raw.MemoryStats.Limit,
		NetworkRxBytes:   rxBytes,
		NetworkTxBytes:   txBytes,
	}, nil
}

// ContainerLogs returns the last tail lines of combined stdout+stderr for containerName.
// When tail <= 0 all available log lines are returned.
func (c *Client) ContainerLogs(ctx context.Context, containerName string, tail int) (string, error) {
	tailStr := "all"
	if tail > 0 {
		tailStr = fmt.Sprintf("%d", tail)
	}
	rc, err := c.inner.ContainerLogs(ctx, containerName, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tailStr,
		Timestamps: true,
	})
	if err != nil {
		return "", fmt.Errorf("logs %s: %w", containerName, err)
	}
	defer rc.Close()

	var stdout, stderr strings.Builder
	if _, err := stdcopy.StdCopy(&stdout, &stderr, rc); err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	return stdout.String() + stderr.String(), nil
}
