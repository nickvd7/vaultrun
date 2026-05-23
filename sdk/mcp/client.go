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
	ID             string  `json:"id"`
	SessionID      string  `json:"session_id"`
	Command        string  `json:"command"`
	Args           []string `json:"args,omitempty"`
	Status         string  `json:"status"`
	ExitCode       *int    `json:"exit_code,omitempty"`
	Stdout         *string `json:"stdout,omitempty"`
	Stderr         *string `json:"stderr,omitempty"`
	DurationMS     int64   `json:"duration_ms,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
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
		var errBody struct{ Error string `json:"error"` }
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
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/files/" + strings.TrimPrefix(filePath, "/")
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errBody struct{ Error string `json:"error"` }
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
