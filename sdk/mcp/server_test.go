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

// The following tests still exercise protocol_legacy.go (serve/handleRequest).
// Prefer sdk_protocol_test.go for behaviour that production runs on the official SDK.
// Keep these only for edge cases the SDK path does not cover (discover, version meta, notifications).

func TestLegacyProtocolServerDiscover(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`10`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "server/discover",
		Params:  json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"0"},"io.modelcontextprotocol/clientCapabilities":{}}}`),
	})
	resp := runMCPRequest(t, srv, req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var disc mcpDiscoverResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &disc); err != nil {
		t.Fatalf("unmarshal discover: %v", err)
	}
	if disc.ResultType != "complete" {
		t.Errorf("resultType: got %q", disc.ResultType)
	}
	if disc.TTLMs <= 0 || disc.CacheScope != "public" {
		t.Errorf("cache hints missing: ttl=%d scope=%q", disc.TTLMs, disc.CacheScope)
	}
	foundCurrent, foundLegacy := false, false
	for _, v := range disc.SupportedVersions {
		if v == protocolVersionCurrent {
			foundCurrent = true
		}
		if v == protocolVersionLegacy {
			foundLegacy = true
		}
	}
	if !foundCurrent || !foundLegacy {
		t.Errorf("supportedVersions=%v, want both current and legacy", disc.SupportedVersions)
	}
	if disc.Capabilities.Tools == nil {
		t.Error("tools capability missing")
	}
	if disc.Meta == nil || disc.Meta[metaKeyServerInfo] == nil {
		t.Error("serverInfo _meta missing")
	}
}

func TestLegacyProtocolUnsupportedVersionInMeta(t *testing.T) {
	srv := newTestServer()
	id := json.RawMessage(`11`)
	req := mustJSON(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "tools/list",
		Params:  json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}`),
	})
	resp := runMCPRequest(t, srv, req)
	if resp.Error == nil {
		t.Fatal("expected unsupported protocol version error")
	}
	if resp.Error.Code != errUnsupportedProtocolVersion {
		t.Errorf("code: got %d want %d", resp.Error.Code, errUnsupportedProtocolVersion)
	}
}

func TestLegacyProtocolUnknownMethod(t *testing.T) {
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

func TestLegacyProtocolNotificationNoResponse(t *testing.T) {
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
