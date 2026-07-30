package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
)

// ReadFile reads a file from a container
func (c *Client) ReadFile(ctx context.Context, containerID, path string) ([]byte, error) {
	// Use CopyFromContainer to get a tar archive
	reader, _, err := c.inner.CopyFromContainer(ctx, containerID, path)
	if err != nil {
		return nil, fmt.Errorf("failed to copy from container: %w", err)
	}
	defer reader.Close()

	// Extract first file from tar
	tr := tar.NewReader(reader)
	header, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("failed to read tar header: %w", err)
	}

	if header.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("expected regular file, got type %d", header.Typeflag)
	}

	// Read file contents
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, tr); err != nil {
		return nil, fmt.Errorf("failed to read file contents: %w", err)
	}

	return buf.Bytes(), nil
}

// WriteFile writes a file to a container
func (c *Client) WriteFile(ctx context.Context, containerID, path string, content []byte) error {
	// Create tar archive in memory
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add file to tar
	header := &tar.Header{
		Name: path,
		Mode: 0644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("failed to write file contents: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}

	// Copy tar to container
	err := c.inner.CopyToContainer(ctx, containerID, "/", &buf, types.CopyToContainerOptions{})
	if err != nil {
		return fmt.Errorf("failed to copy to container: %w", err)
	}

	return nil
}
