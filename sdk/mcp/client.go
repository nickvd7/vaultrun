// VaultRun API client used by the MCP server.
package main

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

type vaultRunClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newVaultRunClient(baseURL, apiKey string) *vaultRunClient {
	return &vaultRunClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *vaultRunClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *vaultRunClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Try to extract "error" field from JSON body.
		var errBody struct {
			Error string `json:"error"`
		}
		if e := json.Unmarshal(raw, &errBody); e == nil && errBody.Error != "" {
			return fmt.Errorf("API error %d: %s", resp.StatusCode, errBody.Error)
		}
		return fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ── API types ─────────────────────────────────────────────────────────────────

type Session struct {
	ID             string            `json:"id"`
	Name           *string           `json:"name,omitempty"`
	Image          string            `json:"image"`
	Status         string            `json:"status"`
	NetworkEnabled bool              `json:"network_enabled"`
	CPULimit       float64           `json:"cpu_limit"`
	MemoryLimitMB  int               `json:"memory_limit_mb"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Labels         map[string]string `json:"labels,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

type Run struct {
	ID              string   `json:"id"`
	SessionID       string   `json:"session_id"`
	Command         string   `json:"command"`
	Args            []string `json:"args,omitempty"`
	Status          string   `json:"status"`
	ExitCode        *int     `json:"exit_code,omitempty"`
	Stdout          *string  `json:"stdout,omitempty"`
	Stderr          *string  `json:"stderr,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	OutputTruncated bool     `json:"output_truncated,omitempty"`
}

type FileEntry struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// ── Sessions ──────────────────────────────────────────────────────────────────

type CreateSessionRequest struct {
	Name           *string           `json:"name,omitempty"`
	Image          string            `json:"image"`
	NetworkEnabled bool              `json:"network_enabled,omitempty"`
	CPULimit       float64           `json:"cpu_limit,omitempty"`
	MemoryLimitMB  int               `json:"memory_limit_mb,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

func (c *vaultRunClient) CreateSession(ctx context.Context, req CreateSessionRequest) (*Session, error) {
	var s Session
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sessions", req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *vaultRunClient) GetSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sessions/"+url.PathEscape(id), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *vaultRunClient) ListSessions(ctx context.Context) ([]Session, error) {
	var result struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sessions?limit=50", nil, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

func (c *vaultRunClient) DeleteSession(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/sessions/"+url.PathEscape(id), nil, nil)
}

// ── Runs ──────────────────────────────────────────────────────────────────────

type RunRequest struct {
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

func (c *vaultRunClient) RunCommand(ctx context.Context, sessionID string, req RunRequest) (*Run, error) {
	var run Run
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(sessionID)+"/run", req, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (c *vaultRunClient) GetRun(ctx context.Context, runID string) (*Run, error) {
	var run Run
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(runID), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// ── Files ─────────────────────────────────────────────────────────────────────

func (c *vaultRunClient) UploadFile(ctx context.Context, sessionID, destPath, content string) (*FileEntry, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile("file", filepath.Base(destPath))
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(fw, content); err != nil {
		return nil, err
	}
	if err := mw.WriteField("path", destPath); err != nil {
		return nil, err
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/files",
		&buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		if e := json.Unmarshal(raw, &errBody); e == nil && errBody.Error != "" {
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, errBody.Error)
		}
		return nil, fmt.Errorf("API error %d", resp.StatusCode)
	}

	var f FileEntry
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (c *vaultRunClient) DownloadFile(ctx context.Context, sessionID, filePath string) (string, error) {
	// URL-escape each path segment individually so that slashes that are part of
	// the path structure are preserved while special characters within a segment
	// (including "..", "?", "#") are percent-encoded.
	segments := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	escapedSegments := make([]string, len(segments))
	for i, s := range segments {
		escapedSegments[i] = url.PathEscape(s)
	}
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/files/" + strings.Join(escapedSegments, "/")
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		if e := json.Unmarshal(body, &errBody); e == nil && errBody.Error != "" {
			return "", fmt.Errorf("API error %d: %s", resp.StatusCode, errBody.Error)
		}
		return "", fmt.Errorf("API error %d", resp.StatusCode)
	}
	return string(body), nil
}

func (c *vaultRunClient) ListFiles(ctx context.Context, sessionID string) ([]FileEntry, error) {
	var result struct {
		Files []FileEntry `json:"files"`
	}
	if err := c.doJSON(ctx, http.MethodGet,
		"/api/v1/sessions/"+url.PathEscape(sessionID)+"/files", nil, &result); err != nil {
		return nil, err
	}
	return result.Files, nil
}

func (c *vaultRunClient) DeleteFile(ctx context.Context, sessionID, filePath string) error {
	segments := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	escapedSegments := make([]string, len(segments))
	for i, s := range segments {
		escapedSegments[i] = url.PathEscape(s)
	}
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/files/" + strings.Join(escapedSegments, "/")
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// ── Runs ─────────────────────────────────────────────────────────────────────

func (c *vaultRunClient) ListRuns(ctx context.Context, sessionID string) ([]Run, error) {
	var result struct {
		Runs []Run `json:"runs"`
	}
	if err := c.doJSON(ctx, http.MethodGet,
		"/api/v1/sessions/"+url.PathEscape(sessionID)+"/runs?limit=50", nil, &result); err != nil {
		return nil, err
	}
	return result.Runs, nil
}

// ── Snapshots ────────────────────────────────────────────────────────────────

type Snapshot struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *vaultRunClient) CreateSnapshot(ctx context.Context, sessionID, name string) (*Snapshot, error) {
	var snap Snapshot
	if err := c.doJSON(ctx, http.MethodPost,
		"/api/v1/sessions/"+url.PathEscape(sessionID)+"/snapshots",
		map[string]string{"name": name}, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (c *vaultRunClient) ListSnapshots(ctx context.Context, sessionID string) ([]Snapshot, error) {
	var result struct {
		Snapshots []Snapshot `json:"snapshots"`
	}
	if err := c.doJSON(ctx, http.MethodGet,
		"/api/v1/sessions/"+url.PathEscape(sessionID)+"/snapshots", nil, &result); err != nil {
		return nil, err
	}
	return result.Snapshots, nil
}

// ── Artifacts ────────────────────────────────────────────────────────────────

type SharedArtifact struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

func (c *vaultRunClient) CreateArtifact(ctx context.Context, sessionID, filePath, name string) (*SharedArtifact, error) {
	body := map[string]string{"path": filePath}
	if name != "" {
		body["name"] = name
	}
	var art SharedArtifact
	if err := c.doJSON(ctx, http.MethodPost,
		"/api/v1/sessions/"+url.PathEscape(sessionID)+"/artifacts", body, &art); err != nil {
		return nil, err
	}
	return &art, nil
}

func (c *vaultRunClient) ListArtifacts(ctx context.Context) ([]SharedArtifact, error) {
	var result struct {
		Artifacts []SharedArtifact `json:"artifacts"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/artifacts?limit=50", nil, &result); err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// ── Audit ────────────────────────────────────────────────────────────────────

type AuditLog struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	SessionID string    `json:"session_id,omitempty"`
}

func (c *vaultRunClient) ListAuditLogs(ctx context.Context, sessionID string, limit int) ([]AuditLog, error) {
	query := fmt.Sprintf("?limit=%d", limit)
	if sessionID != "" {
		query += "&session_id=" + url.QueryEscape(sessionID)
	}
	var result struct {
		AuditLogs []AuditLog `json:"audit_logs"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/audit"+query, nil, &result); err != nil {
		return nil, err
	}
	return result.AuditLogs, nil
}

// ── Docker ────────────────────────────────────────────────────────────────────

type DockerImage struct {
	ID        string    `json:"id"`
	Tags      []string  `json:"tags"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type ContainerStats struct {
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryBytes      uint64  `json:"memory_bytes"`
	MemoryLimitBytes uint64  `json:"memory_limit_bytes"`
	NetworkRxBytes   uint64  `json:"network_rx_bytes"`
	NetworkTxBytes   uint64  `json:"network_tx_bytes"`
}

func (c *vaultRunClient) ListImages(ctx context.Context) ([]DockerImage, error) {
	var result struct {
		Images []DockerImage `json:"images"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/docker/images", nil, &result); err != nil {
		return nil, err
	}
	return result.Images, nil
}

func (c *vaultRunClient) PullImage(ctx context.Context, image string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/v1/docker/images/pull",
		map[string]string{"image": image}, nil)
}

func (c *vaultRunClient) GetSessionStats(ctx context.Context, sessionID string) (*ContainerStats, error) {
	var stats ContainerStats
	if err := c.doJSON(ctx, http.MethodGet,
		"/api/v1/sessions/"+url.PathEscape(sessionID)+"/stats", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

func (c *vaultRunClient) GetSessionLogs(ctx context.Context, sessionID string, tail int) (string, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s/logs?tail=%d", url.PathEscape(sessionID), tail)
	var result struct {
		Logs string `json:"logs"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", err
	}
	return result.Logs, nil
}

// ── Health ───────────────────────────────────────────────────────────────────

func (c *vaultRunClient) HealthCheck(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check failed: HTTP %d", resp.StatusCode)
	}
	return nil
}
