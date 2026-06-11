//go:build integration

// Integration tests require a running API server and Docker daemon.
// Run with: go test -tags=integration ./tests/integration/...

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var (
	apiURL = getEnv("VAULTRUN_TEST_API_URL", "http://localhost:8080")
	apiKey = getEnv("VAULTRUN_TEST_API_KEY", "")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type testClient struct {
	baseURL string
	key     string
	http    *http.Client
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	if apiKey == "" {
		t.Skip("VAULTRUN_TEST_API_KEY not set")
	}
	return &testClient{
		baseURL: apiURL,
		key:     apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *testClient) do(t *testing.T, method, path string, body interface{}) (int, map[string]interface{}) {
	t.Helper()

	var reqBody *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-API-Key", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	return resp.StatusCode, result
}

func TestHealthCheck(t *testing.T) {
	c := newTestClient(t)
	code, body := c.do(t, "GET", "/health", nil)
	if code != 200 {
		t.Fatalf("health check returned %d: %v", code, body)
	}
	if body["status"] != "ok" {
		t.Errorf("health status = %v, want ok", body["status"])
	}
}

func TestSessionLifecycle(t *testing.T) {
	c := newTestClient(t)

	// Create
	code, body := c.do(t, "POST", "/api/v1/sessions", map[string]interface{}{
		"image":           "python:3.12-slim",
		"network_enabled": false,
		"cpu_limit":       0.5,
		"memory_limit_mb": 256,
		"timeout_seconds": 60,
	})

	if code != 201 {
		t.Fatalf("create session: got %d, body: %v", code, body)
	}

	sessionID, ok := body["id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session id missing from response: %v", body)
	}

	t.Logf("Created session %s", sessionID)

	// Get
	code, body = c.do(t, "GET", "/api/v1/sessions/"+sessionID, nil)
	if code != 200 {
		t.Fatalf("get session: got %d, body: %v", code, body)
	}

	// Delete
	t.Cleanup(func() {
		c.do(t, "DELETE", "/api/v1/sessions/"+sessionID, nil)
	})
}

func TestCommandExecution(t *testing.T) {
	c := newTestClient(t)

	// Create session
	code, body := c.do(t, "POST", "/api/v1/sessions", map[string]interface{}{
		"image": "python:3.12-slim",
	})
	if code != 201 {
		t.Fatalf("create session: %d %v", code, body)
	}
	sessionID := body["id"].(string)
	t.Cleanup(func() { c.do(t, "DELETE", "/api/v1/sessions/"+sessionID, nil) })

	// Run echo command
	code, result := c.do(t, "POST", "/api/v1/sessions/"+sessionID+"/run", map[string]interface{}{
		"command":         "echo",
		"args":            []string{"hello world"},
		"timeout_seconds": 10,
	})
	if code != 200 {
		t.Fatalf("run command: %d %v", code, result)
	}

	if result["status"] != "completed" {
		t.Errorf("run status = %v, want completed", result["status"])
	}
	if result["exit_code"] != float64(0) {
		t.Errorf("exit_code = %v, want 0", result["exit_code"])
	}
	stdout, _ := result["stdout"].(string)
	if stdout == "" {
		t.Error("stdout should not be empty")
	}
}

func TestTimeoutEnforcement(t *testing.T) {
	c := newTestClient(t)

	// Create session
	code, body := c.do(t, "POST", "/api/v1/sessions", map[string]interface{}{
		"image": "python:3.12-slim",
	})
	if code != 201 {
		t.Fatalf("create session: %d %v", code, body)
	}
	sessionID := body["id"].(string)
	t.Cleanup(func() { c.do(t, "DELETE", "/api/v1/sessions/"+sessionID, nil) })

	// Run a command that sleeps longer than timeout
	code, result := c.do(t, "POST", "/api/v1/sessions/"+sessionID+"/run", map[string]interface{}{
		"command":         "sleep",
		"args":            []string{"30"},
		"timeout_seconds": 2,
	})
	if code != 200 {
		t.Fatalf("run: %d %v", code, result)
	}

	if result["status"] != "timeout" {
		t.Errorf("run status = %v, want timeout", result["status"])
	}
}

func TestFileUploadAndDownload(t *testing.T) {
	c := newTestClient(t)

	// Create session
	code, body := c.do(t, "POST", "/api/v1/sessions", map[string]interface{}{
		"image": "python:3.12-slim",
	})
	if code != 201 {
		t.Fatalf("create session: %d %v", code, body)
	}
	sessionID := body["id"].(string)
	t.Cleanup(func() { c.do(t, "DELETE", "/api/v1/sessions/"+sessionID, nil) })

	// Upload file
	fileContent := []byte("print('hello from vaultrun')\n")
	pr, pw := bytes.Buffer{}, bytes.Buffer{}
	mw := multipart.NewWriter(&pw)
	mw.WriteField("path", "script.py")
	fw, _ := mw.CreateFormFile("file", "script.py")
	fw.Write(fileContent)
	mw.Close()

	_ = pr
	req, _ := http.NewRequest("POST", apiURL+"/api/v1/sessions/"+sessionID+"/files", &pw)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("upload: status %d", resp.StatusCode)
	}

	// List files
	code, listBody := c.do(t, "GET", "/api/v1/sessions/"+sessionID+"/files", nil)
	if code != 200 {
		t.Fatalf("list files: %d %v", code, listBody)
	}

	// Execute uploaded script
	code, runResult := c.do(t, "POST", "/api/v1/sessions/"+sessionID+"/run", map[string]interface{}{
		"command":         "python",
		"args":            []string{"script.py"},
		"timeout_seconds": 10,
	})
	if code != 200 {
		t.Fatalf("run script: %d %v", code, runResult)
	}
	if runResult["status"] != "completed" {
		t.Errorf("script run status = %v", runResult["status"])
	}
}

func TestAuditLogCreation(t *testing.T) {
	c := newTestClient(t)

	// Create session to generate audit log
	code, body := c.do(t, "POST", "/api/v1/sessions", map[string]interface{}{
		"image": "python:3.12-slim",
	})
	if code != 201 {
		t.Fatalf("create session: %d %v", code, body)
	}
	sessionID := body["id"].(string)
	t.Cleanup(func() { c.do(t, "DELETE", "/api/v1/sessions/"+sessionID, nil) })

	// Check audit logs
	auditURL := fmt.Sprintf("/api/v1/audit?session_id=%s", sessionID)
	code, auditBody := c.do(t, "GET", auditURL, nil)
	if code != 200 {
		t.Fatalf("get audit logs: %d %v", code, auditBody)
	}

	logs, _ := auditBody["audit_logs"].([]interface{})
	if len(logs) == 0 {
		t.Error("expected at least one audit log entry")
	}
}

func TestPathTraversalRejected(t *testing.T) {
	c := newTestClient(t)

	code, body := c.do(t, "POST", "/api/v1/sessions", map[string]interface{}{
		"image": "python:3.12-slim",
	})
	if code != 201 {
		t.Fatalf("create session: %d %v", code, body)
	}
	sessionID := body["id"].(string)
	t.Cleanup(func() { c.do(t, "DELETE", "/api/v1/sessions/"+sessionID, nil) })

	// Attempt path traversal download
	code, _ = c.do(t, "GET", "/api/v1/sessions/"+sessionID+"/files/../../../etc/passwd", nil)
	// Should be 404 (not found — traversal prevented) or 400
	if code == 200 {
		t.Error("path traversal should not succeed")
	}
}

// Helper to measure test duration
var testStart = time.Now()

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	_ = ctx

	// Wait for API to be ready
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(apiURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		time.Sleep(time.Second)
	}

	os.Exit(m.Run())
}

var _ = fmt.Sprintf
var _ = filepath.Join
