package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeClient returns fixed responses for testing without a real VaultRun server.
// We test the protocol layer (JSON-RPC dispatch, tool list) not the API calls.

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func runMCPRequest(t *testing.T, srv *server, reqJSON string) jsonRPCResponse {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(reqJSON + "\n")
	_ = srv.serve(context.Background(), in, &out)

	var resp jsonRPCResponse
	if out.Len() == 0 {
		t.Fatal("server produced no output")
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (raw: %s)", err, out.String())
	}
	return resp
}

func newTestServer() *server {
	return newServer(nil, "python:3.12-slim", "", fsConfig{})
}

func TestProtocolInitialize(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`1`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}`),
	})

	resp := runMCPRequest(t, srv, req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Result should contain protocolVersion and serverInfo.
	var initResult mcpInitializeResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &initResult); err != nil {
		t.Fatalf("unmarshal init result: %v", err)
	}
	if initResult.ProtocolVersion != "2024-11-05" {
		t.Errorf("wrong protocol version: %q", initResult.ProtocolVersion)
	}
	if initResult.ServerInfo.Name != "vaultrun-mcp" {
		t.Errorf("wrong server name: %q", initResult.ServerInfo.Name)
	}
	if initResult.Capabilities.Tools == nil {
		t.Error("tools capability should be set")
	}
}

func TestProtocolToolsList(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`2`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "tools/list",
		Params:  json.RawMessage(`{}`),
	})

	resp := runMCPRequest(t, srv, req)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var listResult mcpToolsListResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &listResult); err != nil {
		t.Fatalf("unmarshal tools list: %v", err)
	}

	wantTools := []string{
		"create_session", "list_sessions", "get_session", "delete_session",
		"run_command", "upload_file", "read_file", "list_files",
		"delete_file", "get_run", "list_runs",
		"create_snapshot", "list_snapshots",
		"create_artifact", "list_artifacts",
		"list_audit_logs",
		"list_images", "pull_image", "get_session_stats", "get_session_logs",
		"run_github_repo", "github_post_comment",
		"fs_read_file", "fs_write_file", "fs_list_dir", "fs_delete_file",
	}
	toolNames := make(map[string]bool)
	for _, tool := range listResult.Tools {
		toolNames[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema.Type == "" {
			t.Errorf("tool %q has empty inputSchema.type", tool.Name)
		}
	}
	for _, want := range wantTools {
		if !toolNames[want] {
			t.Errorf("expected tool %q in tools list", want)
		}
	}
}

func TestProtocolUnknownMethod(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`3`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "bogus/method",
	})

	resp := runMCPRequest(t, srv, req)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("expected errMethodNotFound (%d), got %d", errMethodNotFound, resp.Error.Code)
	}
}

func TestProtocolPing(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`4`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "ping",
	})

	resp := runMCPRequest(t, srv, req)

	if resp.Error != nil {
		t.Fatalf("unexpected error on ping: %+v", resp.Error)
	}
}

func TestProtocolNotificationNoResponse(t *testing.T) {
	// Notifications (no ID) must not produce a response.
	srv := newTestServer()
	var out bytes.Buffer
	notif := `{"jsonrpc":"2.0","method":"initialized"}` + "\n"
	in := strings.NewReader(notif)
	_ = srv.serve(context.Background(), in, &out)
	if out.Len() != 0 {
		t.Errorf("expected no output for notification, got: %s", out.String())
	}
}

func TestToolCallUnknownTool(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`5`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"nonexistent_tool","arguments":{}}`),
	})

	resp := runMCPRequest(t, srv, req)

	// Unknown tool returns a tool result with isError=true, not a JSON-RPC error.
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error (expected tool-level error): %+v", resp.Error)
	}
	var toolResult mcpToolResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &toolResult); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if !toolResult.IsError {
		t.Error("expected isError=true for unknown tool")
	}
	if len(toolResult.Content) == 0 || !strings.Contains(toolResult.Content[0].Text, "nonexistent_tool") {
		t.Errorf("expected error message to mention tool name, got: %v", toolResult.Content)
	}
}

// ── HTTP transport tests ───────────────────────────────────────────────────

func newTestHTTPRouter(token string) (*httptest.Server, func()) {
	srv := newTestServer()
	cfg := httpConfig{
		port:           ":0",
		authToken:      token,
		allowedOrigins: []string{"*"},
		rateLimit:      60,
	}
	// Build the Gin engine but serve via httptest.
	engine := buildHTTPEngine(srv, cfg)
	ts := httptest.NewServer(engine)
	return ts, ts.Close
}

func TestHTTPUnauthorizedWithoutToken(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("secret-token")
	defer cleanup()

	resp, err := http.Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHTTPAuthorizedWithToken(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("secret-token")
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", rpcResp.Error)
	}
}

func TestHTTPSecurityHeaders(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	}
	for header, want := range checks {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("header %s: want %q, got %q", header, want, got)
		}
	}
}

func TestHTTPRateLimit(t *testing.T) {
	srv := newTestServer()
	cfg := httpConfig{
		port:           ":0",
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      2, // very low limit for testing
	}
	engine := buildHTTPEngine(srv, cfg)
	ts := httptest.NewServer(engine)
	defer ts.Close()

	doReq := func() int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer tok")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		return resp.StatusCode
	}

	// First 2 requests should succeed.
	for i := 0; i < 2; i++ {
		if code := doReq(); code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, code)
		}
	}
	// Third request exceeds the limit.
	if code := doReq(); code != http.StatusTooManyRequests {
		t.Errorf("expected 429 after rate limit, got %d", code)
	}
}
