package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/docker/docker/client"
)

// Client wraps the Docker SDK client with optional seccomp enforcement.
type Client struct {
	inner       *client.Client
	seccompJSON string // empty = rely on daemon default; "default" = explicit default; else raw JSON
}

// New creates a new Docker client using the environment / DOCKER_HOST.
//
// If DOCKER_SECCOMP_PROFILE is set it is interpreted as:
//   - "default"      → pass seccomp=default to every container
//   - "/path/to/file" → load the file and embed the JSON in every container's SecurityOpt
//
// An empty value (the default) leaves seccomp handling to the Docker daemon,
// which already applies its built-in default profile in standard installations.
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
		// no-op — daemon applies its own default
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

	return dc, nil
}

func (c *Client) Inner() *client.Client {
	return c.inner
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
