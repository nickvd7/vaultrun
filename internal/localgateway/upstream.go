package localgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatUpstream talks to an OpenAI-compatible inference server.
type ChatUpstream interface {
	ChatCompletions(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	ListModels(ctx context.Context) (*ModelsResponse, error)
}

// HTTPUpstream is the default HTTP client for Ollama / LM Studio / vLLM.
type HTTPUpstream struct {
	baseURL    string
	httpClient *http.Client
}

// NewHTTPUpstream builds an upstream client. The baseURL is operator-configured
// only (validated at config load). Private/loopback destinations are allowed.
func NewHTTPUpstream(baseURL string, timeout time.Duration) *HTTPUpstream {
	return &HTTPUpstream{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Never follow redirects — prevents credential/tool leakage to
				// an unexpected host if the upstream misbehaves.
				return http.ErrUseLastResponse
			},
		},
	}
}

// ChatCompletions POSTs /v1/chat/completions on the upstream.
func (u *HTTPUpstream) ChatCompletions(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := u.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream status %d: %s", resp.StatusCode, truncate(string(raw), 512))
	}
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}
	return &out, nil
}

// ListModels GETs /v1/models when the upstream supports it.
func (u *HTTPUpstream) ListModels(ctx context.Context) (*ModelsResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream models request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream models status %d: %s", resp.StatusCode, truncate(string(raw), 256))
	}
	var out ModelsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
