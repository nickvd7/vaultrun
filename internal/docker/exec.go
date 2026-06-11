package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
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
	Stdout     string // populated by Exec; empty when using ExecStream
	Stderr     string // populated by Exec; empty when using ExecStream
	DurationMS int64
	TimedOut   bool
	Truncated  bool // true when stdout or stderr was capped at MaxOutputBytes (M-10)
}

// Exec runs a command inside the sandbox container and buffers full output.
// It is deliberately NOT shell-invoked — command and args are passed as
// separate fields to the Docker exec API, preventing shell injection.
func (c *Client) Exec(ctx context.Context, cfg ExecConfig) (*ExecResult, error) {
	maxBytes := cfg.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	outW := &limitedWriter{w: &stdoutBuf, limit: maxBytes}
	errW := &limitedWriter{w: &stderrBuf, limit: maxBytes}
	result, err := c.execInternal(ctx, cfg, outW, errW)
	if err != nil {
		return result, err
	}
	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()
	result.Truncated = outW.truncated || errW.truncated
	return result, nil
}

// ExecStream runs a command and writes output chunks to stdout/stderr as they
// arrive. The ExecResult's Stdout and Stderr fields are empty — data is
// written directly to the provided writers.
func (c *Client) ExecStream(ctx context.Context, cfg ExecConfig, stdout, stderr io.Writer) (*ExecResult, error) {
	maxBytes := cfg.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	outW := &limitedWriter{w: stdout, limit: maxBytes}
	errW := &limitedWriter{w: stderr, limit: maxBytes}
	result, err := c.execInternal(ctx, cfg, outW, errW)
	if result != nil {
		result.Truncated = outW.truncated || errW.truncated
	}
	return result, err
}

// execInternal is the shared implementation for Exec and ExecStream.
func (c *Client) execInternal(ctx context.Context, cfg ExecConfig, stdout, stderr io.Writer) (*ExecResult, error) {
	cmd := append([]string{cfg.Command}, cfg.Args...)

	env := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	workingDir := cfg.WorkingDir
	if workingDir == "" {
		workingDir = "/workspace"
	}
	// Restrict working_dir to the workspace or /tmp to prevent access to
	// arbitrary container filesystem paths (e.g. /proc, /etc).
	if workingDir != "/workspace" && workingDir != "/tmp" &&
		!strings.HasPrefix(workingDir, "/workspace/") &&
		!strings.HasPrefix(workingDir, "/tmp/") {
		return nil, fmt.Errorf("working_dir must be within /workspace or /tmp, got: %q", workingDir)
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

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	done := make(chan error, 1)
	go func() {
		done <- demuxDockerStream(resp.Reader, stdout, stderr)
	}()

	timedOut := false
	select {
	case <-timeoutCtx.Done():
		timedOut = true
		resp.Close() // close attach connection to unblock the demux goroutine
		<-done
	case <-done:
	}

	durationMS := time.Since(start).Milliseconds()

	if timedOut {
		return &ExecResult{
			ExitCode:   -1,
			DurationMS: durationMS,
			TimedOut:   true,
		}, nil
	}

	inspect, err := c.inner.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return nil, fmt.Errorf("exec inspect: %w", err)
	}

	return &ExecResult{
		ExitCode:   inspect.ExitCode,
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
	w         io.Writer
	limit     int64
	n         int64
	truncated bool // set when bytes are dropped due to reaching the limit
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.limit - lw.n
	if remaining <= 0 {
		lw.truncated = true
		return len(p), nil // drop excess, signal caller via Truncated
	}
	if int64(len(p)) > remaining {
		lw.truncated = true
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.n += int64(n)
	return n, err
}
