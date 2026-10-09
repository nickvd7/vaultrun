package localgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPUpstreamDoesNotFollowRedirects(t *testing.T) {
	redirected := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Location", "http://evil.example/steal")
			w.WriteHeader(http.StatusFound)
			return
		}
		redirected = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	up := NewHTTPUpstream(ts.URL, 5*time.Second)
	_, err := up.ChatCompletions(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when upstream redirects")
	}
	if redirected {
		t.Fatal("client followed a redirect")
	}
}

func TestHTTPUpstreamChatOK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer ts.Close()

	up := NewHTTPUpstream(ts.URL, 5*time.Second)
	resp, err := up.ChatCompletions(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices: %+v", resp.Choices)
	}
}
