package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type vrClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newVRClient(baseURL, apiKey string) *vrClient {
	return &vrClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 630 * time.Second, // longer than max run time
		},
	}
}

func (c *vrClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

func (c *vrClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		var e struct{ Error string `json:"error"` }
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("VaultRun %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("VaultRun %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type vrSession struct {
	ID string `json:"id"`
}

func (c *vrClient) CreateSession(ctx context.Context, image string, networkEnabled bool, timeoutSec int) (*vrSession, error) {
	body := map[string]any{
		"image":           image,
		"network_enabled": networkEnabled,
		"timeout_seconds": timeoutSec,
	}
	var sess vrSession
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sessions", body, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (c *vrClient) DeleteSession(ctx context.Context, sessionID string) {
	path := "/api/v1/sessions/" + url.PathEscape(sessionID)
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err == nil {
		resp.Body.Close()
	}
}

type vrRunResult struct {
	Stdout   *string `json:"stdout"`
	Stderr   *string `json:"stderr"`
	ExitCode *int    `json:"exit_code"`
}

func (c *vrClient) RunCommand(ctx context.Context, sessionID string, cmd []string, env map[string]string, workingDir string) (*vrRunResult, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("command must not be empty")
	}
	body := map[string]any{
		"command":     cmd[0],
		"args":        cmd[1:],
		"working_dir": workingDir,
	}
	if len(env) > 0 {
		body["env"] = env
	}
	path := "/api/v1/sessions/" + url.PathEscape(sessionID) + "/run"
	var result vrRunResult
	if err := c.doJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// runOutput combines stdout + stderr into a single trimmed string.
func runOutput(r *vrRunResult) string {
	var parts []string
	if r.Stdout != nil && *r.Stdout != "" {
		parts = append(parts, *r.Stdout)
	}
	if r.Stderr != nil && *r.Stderr != "" {
		parts = append(parts, *r.Stderr)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
