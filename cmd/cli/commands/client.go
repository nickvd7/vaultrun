package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type apiClient struct {
	baseURL string
	key     string
	http    *http.Client
}

func newClient() *apiClient {
	return &apiClient{
		baseURL: apiURL,
		key:     apiKey,
		http:    &http.Client{},
	}
}

func (c *apiClient) do(method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("X-API-Key", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
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

func (c *apiClient) get(path string, out interface{}) error {
	return c.do("GET", path, nil, out)
}

func (c *apiClient) post(path string, body, out interface{}) error {
	return c.do("POST", path, body, out)
}

func (c *apiClient) patch(path string, body, out interface{}) error {
	return c.do("PATCH", path, body, out)
}

func (c *apiClient) delete(path string) error {
	return c.do("DELETE", path, nil, nil)
}

// snapshotRecord mirrors the server-side models.Snapshot JSON response.
type snapshotRecord struct {
	ID        uuid.UUID `json:"id"`
	SessionID uuid.UUID `json:"session_id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// artifactRecord mirrors the server-side models.SharedArtifact JSON response.
type artifactRecord struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	SizeBytes   int64      `json:"size_bytes"`
	ContentType string     `json:"content_type"`
	CreatedBy   string     `json:"created_by"`
	SessionID   *uuid.UUID `json:"session_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateSnapshot creates a snapshot of the given session's workspace.
func (c *apiClient) CreateSnapshot(ctx context.Context, sessionID, name string) (*snapshotRecord, error) {
	var snap snapshotRecord
	if err := c.post("/api/v1/sessions/"+sessionID+"/snapshots", map[string]string{"name": name}, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// ListSnapshots lists snapshots for the given session.
func (c *apiClient) ListSnapshots(ctx context.Context, sessionID string) ([]snapshotRecord, error) {
	var result struct {
		Snapshots []snapshotRecord `json:"snapshots"`
	}
	if err := c.get("/api/v1/sessions/"+sessionID+"/snapshots", &result); err != nil {
		return nil, err
	}
	return result.Snapshots, nil
}

// DownloadSnapshot downloads a snapshot archive by ID and returns its bytes.
func (c *apiClient) DownloadSnapshot(ctx context.Context, snapshotID string) ([]byte, error) {
	return c.doRaw("GET", "/api/v1/snapshots/"+snapshotID+"/download")
}

// DeleteSnapshot deletes a snapshot by ID.
func (c *apiClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	return c.delete("/api/v1/snapshots/" + snapshotID)
}

// PromoteArtifact promotes a workspace file to the shared artifact registry.
func (c *apiClient) PromoteArtifact(ctx context.Context, sessionID, filePath, name string) (*artifactRecord, error) {
	body := map[string]string{"path": filePath, "name": name}
	var art artifactRecord
	if err := c.post("/api/v1/sessions/"+sessionID+"/artifacts", body, &art); err != nil {
		return nil, err
	}
	return &art, nil
}

// ListArtifacts lists all shared artifacts.
func (c *apiClient) ListArtifacts(ctx context.Context) ([]artifactRecord, error) {
	var result struct {
		Artifacts []artifactRecord `json:"artifacts"`
	}
	if err := c.get("/api/v1/artifacts", &result); err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// DownloadArtifact downloads an artifact by ID and returns its bytes.
func (c *apiClient) DownloadArtifact(ctx context.Context, artifactID string) ([]byte, error) {
	return c.doRaw("GET", "/api/v1/artifacts/"+artifactID+"/download")
}

// DeleteArtifact deletes an artifact by ID.
func (c *apiClient) DeleteArtifact(ctx context.Context, artifactID string) error {
	return c.delete("/api/v1/artifacts/" + artifactID)
}

// doRaw performs an HTTP request and returns the raw response body.
func (c *apiClient) doRaw(method, path string) ([]byte, error) {
	req, err := http.NewRequest(method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
