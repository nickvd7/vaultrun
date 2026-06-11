// Tests for path sanitization, graceful shutdown, and the /healthz endpoint.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// sanitizePath
// ---------------------------------------------------------------------------

func TestSanitizePath(t *testing.T) {
	good := []string{
		"/workspace/file.txt",
		"/output.csv",
		"/data/sub/result.json",
		"relative/path",
		"/unicode-name.py",
		"file.with.dots",
	}
	for _, p := range good {
		if err := sanitizePath(p); err != nil {
			t.Errorf("sanitizePath(%q) should be accepted: %v", p, err)
		}
	}

	bad := []struct {
		path string
		desc string
	}{
		{"", "empty path"},
		{"../../etc/passwd", "simple traversal"},
		{"/workspace/../../../etc/shadow", "embedded traversal"},
		{"/workspace/sub/../../secrets", "double traversal"},
		{"..", "just dotdot"},
		{"../relative", "relative traversal"},
		{"/foo/..\\bar", "backslash traversal"},
		{"/foo" + string([]byte{0x00}) + "bar", "null byte"},
		{"/foo" + string([]byte{0x1f}) + "bar", "control char 0x1f"},
		{"/foo" + string([]byte{0x7f}) + "bar", "DEL char"},
		{strings.Repeat("a", 4097), "too long"},
	}
	for _, tc := range bad {
		if err := sanitizePath(tc.path); err == nil {
			t.Errorf("sanitizePath should reject %s (%q)", tc.desc, tc.path)
		}
	}
}

// TestSanitizePathNullByte: null bytes cannot appear in Go source string literals
// so we construct the path programmatically.
func TestSanitizePathNullByte(t *testing.T) {
	pathWithNull := "/file" + string([]byte{0x00}) + ".txt"
	if err := sanitizePath(pathWithNull); err == nil {
		t.Error("sanitizePath should reject a path containing a null byte")
	}
}

// TestToolsRejectTraversalPaths: upload_file, read_file, delete_file, and
// create_artifact all return isError=true for traversal paths.
func TestToolsRejectTraversalPaths(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	traversal := "../../etc/passwd"
	cases := []struct {
		tool string
		body string
	}{
		{
			"upload_file",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload_file","arguments":{"session_id":"s","path":"` + traversal + `","content":"x"}}}`,
		},
		{
			"read_file",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"session_id":"s","path":"` + traversal + `"}}}`,
		},
		{
			"delete_file",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_file","arguments":{"session_id":"s","path":"` + traversal + `"}}}`,
		},
		{
			"create_artifact",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_artifact","arguments":{"session_id":"s","file_path":"` + traversal + `"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			status, body := post(t, ts.URL, "Bearer tok", tc.body)
			if status != http.StatusOK {
				t.Fatalf("%s: want HTTP 200 (MCP error result), got %d", tc.tool, status)
			}
			var resp jsonRPCResponse
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			// The error must be surfaced as an MCP tool result with isError=true,
			// not as a JSON-RPC protocol error — so the AI can see and react to it.
			if resp.Error != nil {
				t.Errorf("%s: got JSON-RPC error instead of tool isError result: %+v", tc.tool, resp.Error)
			}
			if resp.Result == nil {
				t.Fatalf("%s: nil result", tc.tool)
			}
			b, _ := json.Marshal(resp.Result)
			var tr mcpToolResult
			if err := json.Unmarshal(b, &tr); err != nil {
				t.Fatalf("unmarshal tool result: %v", err)
			}
			if !tr.IsError {
				t.Errorf("%s: expected isError=true for traversal path, got false (content: %v)", tc.tool, tr.Content)
			}
			// The error message must mention the traversal, not an internal detail.
			if len(tr.Content) == 0 || !strings.Contains(tr.Content[0].Text, "traversal") {
				t.Errorf("%s: expected 'traversal' in error message, got: %v", tc.tool, tr.Content)
			}
		})
	}
}

// TestToolsRejectEmptyPath: empty path is rejected before any API call.
func TestToolsRejectEmptyPath(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	_, err := srv.callTool(ctx, "upload_file", json.RawMessage(
		`{"session_id":"s","path":"","content":"data"}`,
	))
	if err == nil {
		t.Error("empty path should produce an error")
	}
}

// ---------------------------------------------------------------------------
// /healthz endpoint
// ---------------------------------------------------------------------------

// TestHealthzRequiresNoAuth: GET /healthz returns 2xx or 503 without credentials.
func TestHealthzRequiresNoAuth(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Upstream VaultRun API is not running in unit tests, so 503 is also valid.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("healthz without auth: want 200 or 503, got %d", resp.StatusCode)
	}
}

// TestHealthzHasSecurityHeaders: unauthenticated /healthz still gets hardening headers.
func TestHealthzHasSecurityHeaders(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security header X-Content-Type-Options missing on /healthz")
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("security header Content-Security-Policy missing on /healthz")
	}
}

// TestHealthzIsRateLimited: /healthz counts against the global per-IP limit.
func TestHealthzIsRateLimited(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 2)
	defer cleanup()

	var got429 bool
	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
		}
	}
	if !got429 {
		t.Error("expected /healthz to be rate-limited after the global limit is exceeded")
	}
}

// ---------------------------------------------------------------------------
// Per-tool tier rate limiting
// ---------------------------------------------------------------------------

// TestTieredRateLimitHeavyBlockedBeforeRead: with heavy limit=2, run_command is
// blocked on the 3rd attempt while list_sessions (read tier) still succeeds.
func TestTieredRateLimitHeavyBlockedBeforeRead(t *testing.T) {
	eng := buildHTTPEngine(newTestServer(), httpConfig{
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      1000, // global won't interfere
		writeTierLimit: 1000,
		heavyTierLimit: 2, // only 2 run_command per minute
	})
	ts := httptest.NewServer(eng)
	defer ts.Close()

	callTool := func(toolName string) int {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + toolName + `","arguments":{}}}`
		status, _ := post(t, ts.URL, "Bearer tok", body)
		return status
	}

	for i := 0; i < 2; i++ {
		if s := callTool("run_command"); s == http.StatusTooManyRequests {
			t.Fatalf("attempt %d: run_command should not be tier-limited yet, got 429", i+1)
		}
	}
	if s := callTool("run_command"); s != http.StatusTooManyRequests {
		t.Errorf("3rd run_command should be 429 (heavy tier limit), got %d", s)
	}
	// list_sessions (read tier) must still succeed — separate bucket.
	if s := callTool("list_sessions"); s == http.StatusTooManyRequests {
		t.Errorf("list_sessions should not be tier-limited, got 429")
	}
}

// TestTieredRateLimitWriteBlockedBeforeHeavy: write-tier limit applies to
// upload_file independently of the heavy-tier budget for run_command.
func TestTieredRateLimitWriteBlockedBeforeHeavy(t *testing.T) {
	eng := buildHTTPEngine(newTestServer(), httpConfig{
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      1000,
		writeTierLimit: 1, // only 1 upload_file per minute
		heavyTierLimit: 1000,
	})
	ts := httptest.NewServer(eng)
	defer ts.Close()

	callTool := func(toolName string) int {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + toolName + `","arguments":{}}}`
		status, _ := post(t, ts.URL, "Bearer tok", body)
		return status
	}

	if s := callTool("upload_file"); s == http.StatusTooManyRequests {
		t.Fatal("first upload_file should not be rate-limited")
	}
	if s := callTool("upload_file"); s != http.StatusTooManyRequests {
		t.Errorf("second upload_file should be write-tier-limited (429), got %d", s)
	}
	// run_command (heavy tier, separate bucket) must be unaffected.
	if s := callTool("run_command"); s == http.StatusTooManyRequests {
		t.Errorf("run_command should not be blocked by write-tier limit, got 429")
	}
}

// ---------------------------------------------------------------------------
// Graceful shutdown
// ---------------------------------------------------------------------------

// TestGracefulShutdownNormalOperation: server handles requests during normal operation.
func TestGracefulShutdownNormalOperation(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

// TestGracefulShutdownContextCancellationReturnsNil: startHTTPServer returns nil
// (not an error) when the context is cancelled — ErrServerClosed is a clean exit.
func TestGracefulShutdownContextCancellationReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	cfg := httpConfig{
		port:           "127.0.0.1:0",
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      1000,
		writeTierLimit: 500,
		heavyTierLimit: 300,
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		// We can't intercept "server started" easily, so just cancel immediately.
		close(started)
		done <- startHTTPServer(ctx, newTestServer(), cfg)
	}()

	<-started
	// Small delay to let the goroutine reach ListenAndServe, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// nil = clean shutdown; "bind" errors = port conflict (acceptable in CI).
		if err != nil && !strings.Contains(err.Error(), "bind") &&
			!strings.Contains(err.Error(), "address already in use") {
			t.Errorf("startHTTPServer returned unexpected error after cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("startHTTPServer did not return within 10s after ctx cancellation")
	}
}
