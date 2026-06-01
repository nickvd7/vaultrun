package docker

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/docker/docker/client"
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
