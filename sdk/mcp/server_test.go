package main

import (
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

func newTestServer() *server {
	return newServer(nil, "python:3.12-slim", "", fsConfig{})
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
	req.Header.Set("Accept", "application/json, text/event-stream")
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
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for header, want := range checks {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("header %s: want %q, got %q", header, want, got)
		}
	}
	// Gin middleware sets no-store; the official SDK may overwrite with no-cache.
	cc := resp.Header.Get("Cache-Control")
	if cc != "no-store" && !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control: want no-store or no-cache*, got %q", cc)
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
		req.Header.Set("Accept", "application/json, text/event-stream")
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

func modernMeta(version string) string {
	return `{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + version + `","io.modelcontextprotocol/clientInfo":{"name":"test","version":"0"},"io.modelcontextprotocol/clientCapabilities":{}}}`
}

func TestHTTPModernHeadersOK(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":` + modernMeta(protocolVersionCurrent) + `}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("MCP-Protocol-Version", protocolVersionCurrent)
	req.Header.Set("Mcp-Method", "tools/list")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("unexpected error: %+v", rpcResp.Error)
	}
}

func TestHTTPHeaderMismatchMethod(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":` + modernMeta(protocolVersionCurrent) + `}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("MCP-Protocol-Version", protocolVersionCurrent)
	req.Header.Set("Mcp-Method", "ping") // mismatch

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	if rpcResp.Error == nil || rpcResp.Error.Code != errHeaderMismatch {
		t.Fatalf("expected header mismatch error, got %+v", rpcResp.Error)
	}
}

func TestHTTPToolsCallNameHeaderRequired(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	params := `{"name":"list_sessions","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}`
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("MCP-Protocol-Version", protocolVersionCurrent)
	req.Header.Set("Mcp-Method", "tools/call")
	// Missing Mcp-Name

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHTTPModernMethodNotFoundIs404(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	body := `{"jsonrpc":"2.0","id":1,"method":"bogus/method","params":` + modernMeta(protocolVersionCurrent) + `}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("MCP-Protocol-Version", protocolVersionCurrent)
	req.Header.Set("Mcp-Method", "bogus/method")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHTTPLegacyStillWorksWithoutHeaders(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy client expected 200, got %d", resp.StatusCode)
	}
}

func TestDecodeMCPHeaderValueBase64(t *testing.T) {
	got, err := decodeMCPHeaderValue("=?base64?bGlzdF9zZXNzaW9ucw==?=")
	if err != nil {
		t.Fatal(err)
	}
	if got != "list_sessions" {
		t.Errorf("got %q", got)
	}
}
