package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Security: HTTP transport authentication, headers, limits, and logging.
// ---------------------------------------------------------------------------

func newSecTestServer(token string, rateLimit int) (*httptest.Server, func()) {
	eng := buildHTTPEngine(newTestServer(), httpConfig{
		port:           ":0",
		authToken:      token,
		allowedOrigins: []string{"*"},
		rateLimit:      rateLimit,
	})
	ts := httptest.NewServer(eng)
	return ts, ts.Close
}

// post sends an authenticated (or not) POST to /mcp and returns status + body.
func post(t *testing.T, url, authHeader, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const pingBody = `{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`

// TestSecAuthTokenVariations: only the exact "Bearer <token>" (or raw token) is accepted.
func TestSecAuthTokenVariations(t *testing.T) {
	ts, cleanup := newSecTestServer("super-secret-token", 1000)
	defer cleanup()

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"correct bearer", "Bearer super-secret-token", http.StatusOK},
		{"no header at all", "", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"prefix of real token", "Bearer super-secret", http.StatusUnauthorized},
		{"superset of real token", "Bearer super-secret-token-extra", http.StatusUnauthorized},
		{"lowercase scheme", "bearer super-secret-token", http.StatusUnauthorized},
		{"double space after Bearer", "Bearer  super-secret-token", http.StatusUnauthorized},
		{"sql-ish injection token", "Bearer ' OR '1'='1", http.StatusUnauthorized},
		// Note: a trailing space ("Bearer <token> ") is accepted because HTTP
		// transports strip optional trailing whitespace (OWS) from header values
		// before the server ever sees them — this is standards-compliant, not a bypass.
		{"trailing whitespace trimmed by HTTP", "Bearer super-secret-token ", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := post(t, ts.URL, tc.authHeader, pingBody)
			if status != tc.wantStatus {
				t.Errorf("auth %q: want status %d, got %d", tc.authHeader, tc.wantStatus, status)
			}
		})
	}
}

// TestSecDiscoveryRequiresAuth: even GET / is protected.
func TestSecDiscoveryRequiresAuth(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	// Without auth → 401.
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("discovery without auth: want 401, got %d", resp.StatusCode)
	}

	// With auth → 200 and reports the full tool count.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("discovery with auth: want 200, got %d", resp2.StatusCode)
	}
	var info struct {
		ToolsCount int `json:"tools_count"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&info)
	if info.ToolsCount != len(toolDefinitions()) {
		t.Errorf("tools_count: want %d, got %d", len(toolDefinitions()), info.ToolsCount)
	}
}

// TestSecBodySizeLimit: a >4 MB payload is rejected with a parse error, not a crash.
func TestSecBodySizeLimit(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	big := strings.Repeat("a", 4*1024*1024+1024) // just over 4 MB
	body := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + big + `"}}`

	status, respBody := post(t, ts.URL, "Bearer tok", body)
	if status != http.StatusOK {
		t.Errorf("oversized body: want 200 (with JSON-RPC error), got %d", status)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw: %.120s)", err, respBody)
	}
	if resp.Error == nil || resp.Error.Code != errParse {
		t.Errorf("expected parse error for oversized body, got: %+v", resp.Error)
	}
}

// TestSecMalformedRequests: invalid/empty JSON yields graceful parse errors.
func TestSecMalformedRequests(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	for _, body := range []string{`{bad json`, ``, `not json`, `[]`, `null`} {
		status, respBody := post(t, ts.URL, "Bearer tok", body)
		// The server must never 5xx-crash on garbage input. A graceful parse
		// error (200 + JSON-RPC error) or an empty/no-content reply (204, when the
		// payload decodes to a method-less notification) are both acceptable.
		if status != http.StatusOK && status != http.StatusNoContent {
			t.Errorf("body %q: want 200 or 204, got %d", body, status)
			continue
		}
		if respBody != "" {
			var resp jsonRPCResponse
			if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
				t.Errorf("body %q produced non-JSON response: %s", body, respBody)
			}
		}
	}
}

// TestSecNotificationReturns204: a notification (no id) over HTTP gets 204 No Content.
func TestSecNotificationReturns204(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	status, body := post(t, ts.URL, "Bearer tok", `{"jsonrpc":"2.0","method":"initialized"}`)
	if status != http.StatusNoContent {
		t.Errorf("notification: want 204, got %d (body: %s)", status, body)
	}
}

// TestSecGetOnMCPNotAllowed: GET /mcp (authed) has no route → 404, never executes anything.
func TestSecGetOnMCPNotAllowed(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /mcp: want 404, got %d", resp.StatusCode)
	}
}

// TestSecSecurityHeadersOnAllResponses: hardening headers present even on 401.
func TestSecSecurityHeadersOnErrorResponse(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	// Unauthorized request still gets the security headers (middleware runs first).
	status, _ := post(t, ts.URL, "Bearer wrong", pingBody)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", status)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(pingBody))
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers missing on 401 response")
	}
}

// TestSecCORSPreflight: OPTIONS preflight succeeds without auth and echoes CORS headers.
func TestSecCORSPreflight(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/mcp", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight: want 204, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Error("preflight missing Access-Control-Allow-Origin header")
	}
}

// ---------------------------------------------------------------------------
// Security: sensitive parameter redaction in audit logs.
// ---------------------------------------------------------------------------

func TestSecRedactsSensitiveArgsInLog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/mcp", nil)

	params := `{"name":"run_command","arguments":{` +
		`"session_id":"sess-123",` +
		`"command":"python",` +
		`"env":"{\"API_KEY\":\"supersecretvalue\"}",` +
		`"content":"contents-of-a-private-file",` +
		`"secret_env":"{\"DB_PASS\":\"hunter2\"}"}}`
	req := &jsonRPCRequest{Method: "tools/call", Params: json.RawMessage(params)}
	resp := &jsonRPCResponse{JSONRPC: "2.0", Result: mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: "ok"}},
	}}

	logHTTPToolCall(c, req, resp, 5*time.Millisecond)

	out := buf.String()
	for _, secret := range []string{"supersecretvalue", "contents-of-a-private-file", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("secret %q leaked into log: %s", secret, out)
		}
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("expected REDACTED markers in log: %s", out)
	}
	// Non-sensitive fields should still be present for observability.
	if !strings.Contains(out, "run_command") || !strings.Contains(out, "sess-123") {
		t.Errorf("expected tool name and session id in log: %s", out)
	}
}

// ---------------------------------------------------------------------------
// Security: rate limiter correctness and thread-safety.
// ---------------------------------------------------------------------------

// TestSecRateLimiterExactlyLimitAllowedConcurrent: under heavy concurrency,
// exactly `limit` requests are allowed (run with -race to catch data races).
func TestSecRateLimiterExactlyLimitAllowedConcurrent(t *testing.T) {
	const limit = 50
	limiter := newIPRateLimiter(limit)

	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.allow("9.9.9.9") {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	if allowed != limit {
		t.Errorf("expected exactly %d allowed under concurrency, got %d", limit, allowed)
	}
}

// TestSecRateLimiterPerIPIsolation: one IP exhausting its budget does not affect another.
func TestSecRateLimiterPerIPIsolation(t *testing.T) {
	limiter := newIPRateLimiter(1)
	if !limiter.allow("10.0.0.1") {
		t.Fatal("first request from IP-A should be allowed")
	}
	if limiter.allow("10.0.0.1") {
		t.Fatal("second request from IP-A should be blocked")
	}
	if !limiter.allow("10.0.0.2") {
		t.Fatal("IP-B should have its own independent budget")
	}
}

// TestSecRateLimiterWindowExpiry: stale timestamps outside the window are evicted.
func TestSecRateLimiterWindowExpiry(t *testing.T) {
	limiter := newIPRateLimiter(2)
	old := time.Now().Add(-2 * time.Minute) // outside the 1-minute window
	limiter.windows["ip"] = []time.Time{old, old}

	if !limiter.allow("ip") {
		t.Fatal("expired timestamps should be evicted, allow expected true")
	}
	if !limiter.allow("ip") {
		t.Fatal("second allow within fresh window expected true")
	}
	if limiter.allow("ip") {
		t.Fatal("third allow should exceed the limit")
	}
}

// TestSecRateLimiterEvictsDormantIPs: the sweep reclaims memory from expired,
// dormant source IPs (defends against unbounded map growth via IP rotation).
func TestSecRateLimiterEvictsDormantIPs(t *testing.T) {
	limiter := newIPRateLimiter(10)

	// Simulate 5000 one-shot IPs whose single request has already expired.
	old := time.Now().Add(-2 * time.Minute)
	for i := 0; i < 5000; i++ {
		limiter.windows[string(rune(i))+"-dormant"] = []time.Time{old}
	}
	// And one currently-active IP that must survive the sweep.
	limiter.windows["active"] = []time.Time{time.Now()}

	limiter.sweepLocked()

	if _, ok := limiter.windows["active"]; !ok {
		t.Error("active IP should survive the sweep")
	}
	if len(limiter.windows) != 1 {
		t.Errorf("expected all dormant IPs evicted (1 left), got %d entries", len(limiter.windows))
	}
}

// TestSecRateLimiterSweepTriggeredByAllow: allow() opportunistically sweeps.
func TestSecRateLimiterSweepTriggeredByAllow(t *testing.T) {
	limiter := newIPRateLimiter(1000000)
	old := time.Now().Add(-2 * time.Minute)
	limiter.windows["ghost"] = []time.Time{old}

	// Drive enough calls from a live IP to cross the sweep interval.
	for i := 0; i < sweepInterval+1; i++ {
		limiter.allow("live-ip")
	}
	if _, ok := limiter.windows["ghost"]; ok {
		t.Error("dormant 'ghost' IP should have been swept after sweepInterval calls")
	}
}

// TestSecHTTPConcurrentRequests: hammer the real engine concurrently (run with -race).
func TestSecHTTPConcurrentRequests(t *testing.T) {
	ts, cleanup := newSecTestServer("tok", 1000000)
	defer cleanup()

	var wg sync.WaitGroup
	var ok int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
				strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			req.Header.Set("Authorization", "Bearer tok")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			if resp.StatusCode == http.StatusOK {
				atomic.AddInt64(&ok, 1)
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()
	if ok != 64 {
		t.Errorf("expected all 64 concurrent requests to succeed, got %d", ok)
	}
}

// TestSecHTTPConfigRejectsMissingToken: HTTP transport refuses to start without a token.
func TestSecHTTPConfigRejectsMissingToken(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", "")
	_, err := httpConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "MCP_AUTH_TOKEN") {
		t.Errorf("expected config to reject missing token, got: %v", err)
	}
}

// TestSecHTTPConfigRejectsPartialTLS: TLS cert without key (or vice versa) is rejected.
func TestSecHTTPConfigRejectsPartialTLS(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", "tok")
	t.Setenv("MCP_TLS_CERT", "/tmp/does-not-exist.crt")
	t.Setenv("MCP_TLS_KEY", "")
	_, err := httpConfigFromEnv()
	if err == nil {
		t.Error("expected error when only one of cert/key is set")
	}
}
