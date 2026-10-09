package localgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthRequiredAndConstantTimeReject(t *testing.T) {
	g := New(testConfig(), newMockVR(), &mockUpstream{})
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	// No auth
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no auth: got %d", resp.StatusCode)
	}

	// Wrong token
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong-token-16char")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong auth: got %d", resp.StatusCode)
	}

	// healthz unauthenticated
	resp, err = http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: got %d", resp.StatusCode)
	}
}

func TestRateLimitBeforeAuth(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerMin = 2
	g := New(cfg, newMockVR(), &mockUpstream{})
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer wrong-guess-token1")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, resp.StatusCode)
		resp.Body.Close()
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on third request, got %v", codes)
	}
}

func TestXForwardedForNotTrusted(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerMin = 2
	g := New(cfg, newMockVR(), &mockUpstream{})
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer wrong-guess-token1")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "1.2.3."+string(rune('0'+i)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		code := resp.StatusCode
		resp.Body.Close()
		if i == 2 && code != http.StatusTooManyRequests {
			t.Fatalf("XFF rotation bypassed rate limit: codes ended with %d", code)
		}
	}
}

func TestBodySizeLimit(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBodyBytes = 1024
	g := New(cfg, newMockVR(), &mockUpstream{})
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	body := `{"model":"llama","messages":[{"role":"user","content":"` + strings.Repeat("a", 2000) + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

func TestChatCompletionsHappyPathHeaders(t *testing.T) {
	vr := newMockVR()
	up := &mockUpstream{
		responses: []*ChatResponse{{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "hi"},
				FinishReason: "stop",
			}},
		}},
	}
	cfg := testConfig()
	g := New(cfg, vr, up)
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	payload := map[string]any{
		"model":    "llama",
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerConversationID, "demo-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if resp.Header.Get(headerSessionID) == "" {
		t.Fatal("expected X-VaultRun-Session-Id response header")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing security headers")
	}
}

func TestInvalidConversationIDRejected(t *testing.T) {
	cfg := testConfig()
	g := New(cfg, newMockVR(), &mockUpstream{})
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"llama","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerConversationID, "bad id with spaces!")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSecurityHeadersOnUnauthorized(t *testing.T) {
	g := New(testConfig(), newMockVR(), &mockUpstream{})
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("CSP missing on 401")
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("Cache-Control missing")
	}
}
