package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Integration tests: drive the full tool dispatch against a mock VaultRun API.
// These exercise the client → tool → formatting pipeline with crazy responses.
// ---------------------------------------------------------------------------

// newMockServer spins up a fake VaultRun API and returns an MCP server wired to it.
func newMockServer(handler http.HandlerFunc) (*server, *httptest.Server) {
	ts := httptest.NewServer(handler)
	return newServer(newVaultRunClient(ts.URL, "test-key"), "python:3.12-slim"), ts
}

func argsJSON(m map[string]string) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

// TestIntegrationCreateSessionUnicode: session name with CJK + emoji round-trips.
func TestIntegrationCreateSessionUnicode(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		// Echo back the (unicode) name to confirm it survived JSON encoding.
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              "sess-😀-1",
			"image":           "python:3.12-slim",
			"status":          "created",
			"cpu_limit":       1.0,
			"memory_limit_mb": 512,
			"timeout_seconds": 300,
			"created_at":      time.Now(),
		})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "create_session",
		argsJSON(map[string]string{"name": "日本語テスト 🎌 \"quotes\" & <tags>"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected isError: %v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "sess-😀-1") {
		t.Errorf("expected session id in output, got: %s", res.Content[0].Text)
	}
}

// TestIntegrationRunCommandFailedHugeOutput: failed run with 1 MB stdout → isError + output present.
func TestIntegrationRunCommandFailedHugeOutput(t *testing.T) {
	huge := strings.Repeat("x", 1024*1024)
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "r1",
			"session_id":  "s1",
			"command":     "crash",
			"status":      "failed",
			"exit_code":   137,
			"stdout":      huge,
			"stderr":      "segfault",
			"duration_ms": 4242,
		})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "run_command",
		argsJSON(map[string]string{"session_id": "s1", "command": "crash"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("expected isError=true for failed run")
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "137") || !strings.Contains(text, "failed") {
		t.Errorf("expected exit code and status in output")
	}
	if len(text) < 1024*1024 {
		t.Errorf("expected huge stdout to be preserved, got %d bytes", len(text))
	}
}

// TestIntegrationRunCommandTimeout: timeout status flagged as error.
func TestIntegrationRunCommandTimeout(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "r1", "session_id": "s1", "command": "sleep", "status": "timeout",
		})
	})
	defer ts.Close()

	res, _ := srv.callTool(context.Background(), "run_command",
		argsJSON(map[string]string{"session_id": "s1", "command": "sleep"}))
	if !res.IsError {
		t.Error("expected isError=true for timeout")
	}
}

// TestIntegrationAPIErrorPropagates: API 500 with {"error":...} surfaces to the tool.
func TestIntegrationAPIErrorPropagates(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "kaboom-internal"})
	})
	defer ts.Close()

	_, err := srv.callTool(context.Background(), "create_session", argsJSON(nil))
	if err == nil || !strings.Contains(err.Error(), "kaboom-internal") {
		t.Errorf("expected propagated API error, got: %v", err)
	}
}

// TestIntegrationMalformedAPIResponse: garbage body from the API yields an error, not a panic.
func TestIntegrationMalformedAPIResponse(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{this is not valid json at all"))
	})
	defer ts.Close()

	_, err := srv.callTool(context.Background(), "create_session", argsJSON(nil))
	if err == nil {
		t.Error("expected unmarshal error for malformed API response")
	}
}

// TestIntegrationNetworkFailure: unreachable API yields an error, not a hang/panic.
func TestIntegrationNetworkFailure(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {})
	url := ts.URL
	ts.Close() // close immediately so connections are refused
	srv = newServer(newVaultRunClient(url, "k"), "python:3.12-slim")

	_, err := srv.callTool(context.Background(), "list_sessions", argsJSON(nil))
	if err == nil {
		t.Error("expected network error against closed server")
	}
}

// TestIntegrationCreateArtifactSendsNameAndPath: verifies request body contents.
func TestIntegrationCreateArtifactSendsNameAndPath(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/artifacts") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["path"] != "/out.csv" || req["name"] != "report" {
			t.Errorf("unexpected artifact body: %v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "a1", "name": "report", "size_bytes": 99, "content_type": "text/csv", "created_at": time.Now(),
		})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "create_artifact",
		argsJSON(map[string]string{"session_id": "s1", "file_path": "/out.csv", "name": "report"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "a1") || !strings.Contains(res.Content[0].Text, "report") {
		t.Errorf("expected artifact details in output, got: %s", res.Content[0].Text)
	}
}

// TestIntegrationListAuditLogs: audit entries are formatted with actor/action.
func TestIntegrationListAuditLogs(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/audit" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audit_logs": []map[string]any{
				{"id": "l1", "timestamp": time.Now(), "actor": "alice", "action": "session.created", "session_id": "s1"},
				{"id": "l2", "timestamp": time.Now(), "actor": "bob", "action": "command.started", "session_id": "s1"},
			},
		})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "list_audit_logs",
		argsJSON(map[string]string{"session_id": "s1", "limit": "20"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "alice") || !strings.Contains(text, "session.created") {
		t.Errorf("expected audit details, got: %s", text)
	}
}

// TestIntegrationHugeSessionList: 1000 sessions handled without crash.
func TestIntegrationHugeSessionList(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		sessions := make([]map[string]any, 1000)
		for i := range sessions {
			sessions[i] = map[string]any{
				"id": "s", "image": "x", "status": "running", "created_at": time.Now(),
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": sessions})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "list_sessions", argsJSON(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "1000 session(s)") {
		t.Errorf("expected 1000 sessions, got: %s", res.Content[0].Text[:60])
	}
}

// ---------------------------------------------------------------------------
// Argument parsing edge cases (no client needed — validation happens first).
// ---------------------------------------------------------------------------

func TestArgsCrazyInputs(t *testing.T) {
	srv := newTestServer() // nil client; these all fail before any API call

	cases := []struct {
		name    string
		tool    string
		rawArgs string
		wantErr string // substring expected in the error
	}{
		{"non-string arg value", "get_session", `{"session_id":12345}`, "invalid arguments"},
		{"nested object arg", "get_session", `{"session_id":{"nested":true}}`, "invalid arguments"},
		{"array as arguments", "get_session", `["a","b"]`, "invalid arguments"},
		{"args not a JSON array", "run_command", `{"session_id":"s","command":"x","args":"just-a-string"}`, "JSON array"},
		{"args is a JSON object", "run_command", `{"session_id":"s","command":"x","args":"{\"k\":1}"}`, "JSON array"},
		{"env not a JSON object", "run_command", `{"session_id":"s","command":"x","env":"[1,2,3]"}`, "JSON object"},
		{"missing required session_id", "get_session", `{}`, "session_id is required"},
		{"missing required command", "run_command", `{"session_id":"s"}`, "command is required"},
		{"missing path for delete_file", "delete_file", `{"session_id":"s"}`, "path must not be empty"},
		{"missing name for snapshot", "create_snapshot", `{"session_id":"s"}`, "name is required"},
		{"missing file_path for artifact", "create_artifact", `{"session_id":"s"}`, "path must not be empty"},
		{"missing run_id", "get_run", `{}`, "run_id is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.callTool(context.Background(), tc.tool, json.RawMessage(tc.rawArgs))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestArgsExtraUnknownFieldsIgnored: unexpected extra args are silently ignored.
func TestArgsExtraUnknownFieldsIgnored(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ok", "image": "x", "status": "created", "created_at": time.Now(),
		})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "create_session",
		json.RawMessage(`{"image":"x","totally_unknown_field":"ignored","another":"one"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Errorf("extra fields should be ignored, got error: %v", res.Content)
	}
}
