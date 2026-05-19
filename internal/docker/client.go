package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/client"
)

// Client wraps the Docker SDK client.
type Client struct {
	inner *client.Client
}

// New creates a new Docker client using the environment / DOCKER_HOST.
func New() (*Client, error) {
	c, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{inner: c}, nil
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
