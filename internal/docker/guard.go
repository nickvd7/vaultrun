package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// The Docker guard (internal/dockerguard, the docker-guard service in
// deployments/docker-compose.yml) holds the Docker socket instead of the API
// and forwards only what the API needs. It also does the few privileged things
// the API may not do itself; these are its /vaultrun/* endpoints.

// GuardProbePath answers 200 on a guard and 404 on a Docker daemon.
const GuardProbePath = "/vaultrun/guard"

// GuardWorkspaceRequest asks the guard for a workspace directory (POST
// /vaultrun/workspaces); DELETE /vaultrun/workspaces/<name> removes one.
type GuardWorkspaceRequest struct {
	Name string      `json:"name"`
	Mode os.FileMode `json:"mode"`
}

// GuardQuotaRequest asks the guard to check the workspace base for XFS
// project quotas ("check") or to give a workspace a project and a limit
// ("set") (POST /vaultrun/xfs-quota).
type GuardQuotaRequest struct {
	Op      string `json:"op"`
	Name    string `json:"name,omitempty"`
	Project uint32 `json:"project,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
}

type guardState struct {
	mu      sync.Mutex
	checked bool
	on      bool
}

// Guarded reports whether DOCKER_HOST is a Docker guard rather than the
// daemon. The answer is cached once the endpoint has answered.
func (c *Client) Guarded(ctx context.Context) bool {
	c.guard.mu.Lock()
	defer c.guard.mu.Unlock()
	if c.guard.checked {
		return c.guard.on
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := c.guardRequest(ctx, http.MethodGet, GuardProbePath, nil, nil)
	if err != nil && status == 0 {
		return false // not reachable: ask again next time
	}
	c.guard.checked, c.guard.on = true, status == http.StatusOK
	return c.guard.on
}

// guardRequest sends a request to a guard endpoint over the Docker client's
// connection and decodes a JSON answer into out.
func (c *Client) guardRequest(ctx context.Context, method, path string, body, out any) (int, error) {
	base := "http://docker"
	if u, err := url.Parse(c.inner.DaemonHost()); err == nil && (u.Scheme == "tcp" || u.Scheme == "http") {
		base = "http://" + u.Host
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.inner.HTTPClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(string(data))
		}
		return resp.StatusCode, fmt.Errorf("%s", e.Message)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// MakeWorkspace has the guard create a directory in the workspace base, owned
// by the API's user.
func (c *Client) MakeWorkspace(name string, mode os.FileMode) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.guardRequest(ctx, http.MethodPost, "/vaultrun/workspaces", GuardWorkspaceRequest{Name: name, Mode: mode}, nil)
	return err
}

// RemoveWorkspace has the guard remove a directory from the workspace base.
func (c *Client) RemoveWorkspace(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_, err := c.guardRequest(ctx, http.MethodDelete, "/vaultrun/workspaces/"+url.PathEscape(name), nil, nil)
	return err
}

// GuardQuota sends an XFS quota request to the guard.
func (c *Client) GuardQuota(ctx context.Context, req GuardQuotaRequest) error {
	_, err := c.guardRequest(ctx, http.MethodPost, "/vaultrun/xfs-quota", req, nil)
	return err
}
