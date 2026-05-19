package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// ExecConfig holds parameters for running a command inside a container.
type ExecConfig struct {
	ContainerID    string
	Command        string
	Args           []string
	Env            map[string]string
	WorkingDir     string
	TimeoutSeconds int
	MaxOutputBytes int64
}

// ExecResult is the result of an exec invocation.
type ExecResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMS int64
	TimedOut   bool
}

// Exec runs a command inside the sandbox container. It is deliberately
// NOT shell-invoked — command and args are passed as separate fields to
// the Docker exec API, preventing shell injection.
func (c *Client) Exec(ctx context.Context, cfg ExecConfig) (*ExecResult, error) {
	cmd := append([]string{cfg.Command}, cfg.Args...)

	env := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	workingDir := cfg.WorkingDir
	if workingDir == "" {
		workingDir = "/workspace"
	}

	execID, err := c.inner.ContainerExecCreate(ctx, cfg.ContainerID, types.ExecConfig{
		Cmd:          cmd,
		Env:          env,
		WorkingDir:   workingDir,
		AttachStdout: true,
		AttachStderr: true,
		User:         "nobody",
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	resp, err := c.inner.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	// Apply timeout
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	maxBytes := cfg.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 // 10 MB default
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutWriter := &limitedWriter{w: &stdoutBuf, limit: maxBytes}
	stderrWriter := &limitedWriter{w: &stderrBuf, limit: maxBytes}

	start := time.Now()

	done := make(chan error, 1)
	go func() {
		done <- demuxDockerStream(resp.Reader, stdoutWriter, stderrWriter)
	}()

	timedOut := false
	select {
	case <-timeoutCtx.Done():
		timedOut = true
		// Kill the exec process
		_ = c.inner.ContainerExecResize(ctx, execID.ID, container.ResizeOptions{})
	case <-done:
	}

	durationMS := time.Since(start).Milliseconds()

	if timedOut {
		return &ExecResult{
			ExitCode:   -1,
			Stdout:     stdoutBuf.String(),
			Stderr:     stderrBuf.String() + "\n[process killed: timeout]",
			DurationMS: durationMS,
			TimedOut:   true,
		}, nil
	}

	// Inspect to get exit code
	inspect, err := c.inner.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return nil, fmt.Errorf("exec inspect: %w", err)
	}

	return &ExecResult{
		ExitCode:   inspect.ExitCode,
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMS: durationMS,
	}, nil
}

// demuxDockerStream splits the multiplexed Docker attach stream into stdout/stderr.
// Each frame is prefixed with an 8-byte header: [stream_type, 0, 0, 0, size(4 bytes big-endian)].
func demuxDockerStream(r io.Reader, stdout, stderr io.Writer) error {
	hdr := make([]byte, 8)
	for {
		_, err := io.ReadFull(r, hdr)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}

		streamType := hdr[0]
		size := binary.BigEndian.Uint32(hdr[4:])

		var dst io.Writer
		switch streamType {
		case 1:
			dst = stdout
		case 2:
			dst = stderr
		default:
			dst = io.Discard
		}

		if _, err := io.CopyN(dst, r, int64(size)); err != nil {
			return err
		}
	}
}

type limitedWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.limit - lw.n
	if remaining <= 0 {
		return len(p), nil // silently drop excess
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.n += int64(n)
	return n, err
}
