// Package vaultrun provides a Go SDK for the VaultRun API.
package vaultrun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Client is the VaultRun API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new VaultRun client.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Session represents a VaultRun sandbox session.
type Session struct {
	ID             string            `json:"id"`
	Name           *string           `json:"name,omitempty"`
	Image          string            `json:"image"`
	Status         string            `json:"status"`
	ContainerID    *string           `json:"container_id,omitempty"`
	NetworkEnabled bool              `json:"network_enabled"`
	CPULimit       float64           `json:"cpu_limit"`
	MemoryLimitMB  int               `json:"memory_limit_mb"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Labels         map[string]string `json:"labels,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	StoppedAt      *time.Time        `json:"stopped_at,omitempty"`
}

// Run represents a command execution result.
type Run struct {
	ID              string     `json:"id"`
	SessionID       string     `json:"session_id"`
	Command         string     `json:"command"`
	Args            []string   `json:"args"`
	Status          string     `json:"status"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	Stdout          *string    `json:"stdout,omitempty"`
	Stderr          *string    `json:"stderr,omitempty"`
	DurationMS      *int64     `json:"duration_ms,omitempty"`
	TimeoutSeconds  int        `json:"timeout_seconds"`
	OutputTruncated bool       `json:"output_truncated,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

// File represents a file in a session workspace.
type File struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateSessionOptions configures a new session.
type CreateSessionOptions struct {
	Name           string
	Image          string
	NetworkEnabled bool
	CPULimit       float64
	MemoryLimitMB  int
	TimeoutSeconds int
	Labels         map[string]string
}

// CreateSession creates a new sandbox session.
func (c *Client) CreateSession(ctx context.Context, opts CreateSessionOptions) (*Session, error) {
	body := map[string]interface{}{
		"image":           opts.Image,
		"network_enabled": opts.NetworkEnabled,
		"cpu_limit":       opts.CPULimit,
		"memory_limit_mb": opts.MemoryLimitMB,
		"timeout_seconds": opts.TimeoutSeconds,
	}
	if opts.Name != "" {
		body["name"] = opts.Name
	}
	if opts.Image == "" {
		body["image"] = "python:3.12-slim"
	}
	if len(opts.Labels) > 0 {
		body["labels"] = opts.Labels
	}

	var s Session
	if err := c.do(ctx, "POST", "/api/v1/sessions", body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSession retrieves a session by ID.
func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var s Session
	if err := c.do(ctx, "GET", "/api/v1/sessions/"+sessionID, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSessionsOptions filters the session list.
type ListSessionsOptions struct {
	// LabelKey and LabelValue filter by a specific label (both must be set).
	LabelKey   string
	LabelValue string
}

// ListSessions returns active sessions visible to the authenticated caller.
func (c *Client) ListSessions(ctx context.Context, opts ...ListSessionsOptions) ([]*Session, error) {
	path := "/api/v1/sessions"
	if len(opts) > 0 && opts[0].LabelKey != "" {
		q := url.Values{}
		q.Set("label", opts[0].LabelKey+":"+opts[0].LabelValue)
		path += "?" + q.Encode()
	}
	var result struct {
		Sessions []*Session `json:"sessions"`
	}
	if err := c.do(ctx, "GET", path, nil, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

// DeleteSession stops and removes a session.
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, "DELETE", "/api/v1/sessions/"+sessionID, nil, nil)
}

// UpdateSessionLabels replaces the label set on a session.
// Pass an empty map to clear all labels.
func (c *Client) UpdateSessionLabels(ctx context.Context, sessionID string, labels map[string]string) error {
	if labels == nil {
		labels = map[string]string{}
	}
	return c.do(ctx, "PATCH", "/api/v1/sessions/"+sessionID+"/labels",
		map[string]interface{}{"labels": labels}, nil)
}

// RunOptions configures a command execution.
type RunOptions struct {
	Command        string
	Args           []string
	Env            map[string]string
	WorkingDir     string
	TimeoutSeconds int
}

// Run executes a command inside a session and waits for the result.
func (c *Client) Run(ctx context.Context, sessionID string, opts RunOptions) (*Run, error) {
	body := map[string]interface{}{
		"command":         opts.Command,
		"args":            opts.Args,
		"timeout_seconds": opts.TimeoutSeconds,
	}
	if opts.Env != nil {
		body["env"] = opts.Env
	}
	if opts.WorkingDir != "" {
		body["working_dir"] = opts.WorkingDir
	}

	var r Run
	if err := c.do(ctx, "POST", "/api/v1/sessions/"+sessionID+"/run", body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// AsyncRunResult is returned by RunAsync — contains the pending run's ID.
type AsyncRunResult struct {
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// AsyncRunOptions extends RunOptions with an optional webhook callback URL.
type AsyncRunOptions struct {
	RunOptions
	// CallbackURL receives an HTTP POST with the completed Run when the job finishes.
	CallbackURL string
}

// RunAsync submits a command for non-blocking execution. It returns immediately
// with the pending run's ID. Poll GetRun to check for completion, or supply a
// CallbackURL to receive a webhook when done.
func (c *Client) RunAsync(ctx context.Context, sessionID string, opts AsyncRunOptions) (*AsyncRunResult, error) {
	body := map[string]interface{}{
		"command":         opts.Command,
		"args":            opts.Args,
		"timeout_seconds": opts.TimeoutSeconds,
	}
	if opts.Env != nil {
		body["env"] = opts.Env
	}
	if opts.WorkingDir != "" {
		body["working_dir"] = opts.WorkingDir
	}
	if opts.CallbackURL != "" {
		body["callback_url"] = opts.CallbackURL
	}

	var result AsyncRunResult
	if err := c.do(ctx, "POST", "/api/v1/sessions/"+sessionID+"/run/async", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRun retrieves a run by ID.
func (c *Client) GetRun(ctx context.Context, runID string) (*Run, error) {
	var r Run
	if err := c.do(ctx, "GET", "/api/v1/runs/"+runID, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns all runs for a session.
func (c *Client) ListRuns(ctx context.Context, sessionID string) ([]*Run, error) {
	var result struct {
		Runs []*Run `json:"runs"`
	}
	if err := c.do(ctx, "GET", "/api/v1/sessions/"+sessionID+"/runs", nil, &result); err != nil {
		return nil, err
	}
	return result.Runs, nil
}

// UploadFile uploads a file to a session workspace.
func (c *Client) UploadFile(ctx context.Context, sessionID, remotePath string, content io.Reader) (*File, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		defer mw.Close()

		if err := mw.WriteField("path", remotePath); err != nil {
			errCh <- err
			return
		}

		fw, err := mw.CreateFormFile("file", filepath.Base(remotePath))
		if err != nil {
			errCh <- err
			return
		}

		if _, err := io.Copy(fw, content); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v1/sessions/"+sessionID+"/files", pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := <-errCh; err != nil {
		return nil, err
	}

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var f File
	if err := json.Unmarshal(respBody, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// DownloadFile downloads a single file from a session workspace.
func (c *Client) DownloadFile(ctx context.Context, sessionID, remotePath string) (io.ReadCloser, error) {
	// Strip leading slash so the URL path doesn't double-slash
	clean := strings.TrimPrefix(remotePath, "/")
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.baseURL+"/api/v1/sessions/"+sessionID+"/files/"+clean, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// DownloadWorkspace downloads the entire workspace as a ZIP archive.
// The caller is responsible for closing the returned ReadCloser.
func (c *Client) DownloadWorkspace(ctx context.Context, sessionID string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		c.baseURL+"/api/v1/sessions/"+sessionID+"/workspace.zip", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)

	// Use a client without timeout for potentially large archives.
	noTimeoutClient := *c.httpClient
	noTimeoutClient.Timeout = 0
	resp, err := noTimeoutClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("workspace download failed (%d): %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// ListFiles returns all files in a session workspace.
func (c *Client) ListFiles(ctx context.Context, sessionID string) ([]*File, error) {
	var result struct {
		Files []*File `json:"files"`
	}
	if err := c.do(ctx, "GET", "/api/v1/sessions/"+sessionID+"/files", nil, &result); err != nil {
		return nil, err
	}
	return result.Files, nil
}

// DeleteFile removes a file from a session workspace.
func (c *Client) DeleteFile(ctx context.Context, sessionID, remotePath string) error {
	clean := strings.TrimPrefix(remotePath, "/")
	return c.do(ctx, "DELETE", "/api/v1/sessions/"+sessionID+"/files/"+clean, nil, nil)
}

// APIKey represents a VaultRun API key (plaintext is never returned after creation).
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Active     bool       `json:"active"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// CreatedKey is returned once on key creation — it includes the plaintext key.
type CreatedKey struct {
	APIKey
	Key string `json:"key"`
}

// ListKeys returns all API keys.
func (c *Client) ListKeys(ctx context.Context) ([]*APIKey, error) {
	var result struct {
		APIKeys []*APIKey `json:"api_keys"`
	}
	if err := c.do(ctx, "GET", "/api/v1/keys", nil, &result); err != nil {
		return nil, err
	}
	return result.APIKeys, nil
}

// CreateKeyOptions configures a new API key.
type CreateKeyOptions struct {
	Name      string
	ExpiresAt *time.Time // nil means no expiry
}

// CreateKey creates a new API key. The plaintext key is returned in
// CreatedKey.Key and will never be retrievable again.
func (c *Client) CreateKey(ctx context.Context, opts CreateKeyOptions) (*CreatedKey, error) {
	body := map[string]interface{}{"name": opts.Name}
	if opts.ExpiresAt != nil {
		body["expires_at"] = opts.ExpiresAt.UTC().Format(time.RFC3339)
	}
	var k CreatedKey
	if err := c.do(ctx, "POST", "/api/v1/keys", body, &k); err != nil {
		return nil, err
	}
	return &k, nil
}

// RevokeKey deactivates an API key by ID.
func (c *Client) RevokeKey(ctx context.Context, keyID string) error {
	return c.do(ctx, "DELETE", "/api/v1/keys/"+keyID, nil, nil)
}

// StreamEvent is a single SSE event from the run/stream endpoint.
type StreamEvent struct {
	Type       string `json:"type"`        // "stdout", "stderr", or "done"
	Data       string `json:"data"`        // populated for stdout/stderr
	RunID      string `json:"run_id"`      // populated in done
	Status     string `json:"status"`      // populated in done
	ExitCode   *int   `json:"exit_code"`   // populated in done
	DurationMS *int64 `json:"duration_ms"` // populated in done
	Error      string `json:"error"`       // populated on error
}

// Stream executes a command via SSE, writing stdout/stderr chunks to the
// provided writers as they arrive. Returns the final StreamEvent (type="done").
// Pass nil for stdout or stderr to discard that stream.
func (c *Client) Stream(ctx context.Context, sessionID string, opts RunOptions, stdout, stderr io.Writer) (*StreamEvent, error) {
	body := map[string]interface{}{
		"command":         opts.Command,
		"args":            opts.Args,
		"timeout_seconds": opts.TimeoutSeconds,
	}
	if opts.Env != nil {
		body["env"] = opts.Env
	}
	if opts.WorkingDir != "" {
		body["working_dir"] = opts.WorkingDir
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/api/v1/sessions/"+sessionID+"/run/stream", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// Use a longer timeout for streaming — the caller controls cancellation via ctx.
	streamClient := *c.httpClient
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body2, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("stream error %d: %s", resp.StatusCode, string(body2))
	}

	// Read SSE lines: "data: <json>\n\n"
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 512)
	for {
		n, readErr := resp.Body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			// Process all complete lines
			for {
				nl := bytes.IndexByte(buf, '\n')
				if nl < 0 {
					break
				}
				line := buf[:nl]
				buf = buf[nl+1:]
				if bytes.HasPrefix(line, []byte("data: ")) {
					payload := line[6:]
					var ev StreamEvent
					if err := json.Unmarshal(payload, &ev); err != nil {
						continue
					}
					switch ev.Type {
					case "stdout":
						if stdout != nil {
							_, _ = io.WriteString(stdout, ev.Data)
						}
					case "stderr":
						if stderr != nil {
							_, _ = io.WriteString(stderr, ev.Data)
						}
					case "done":
						return &ev, nil
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return nil, fmt.Errorf("stream ended without done event")
}

// --- internal helpers ---

func (c *Client) do(ctx context.Context, method, path string, body, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("api error %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}
