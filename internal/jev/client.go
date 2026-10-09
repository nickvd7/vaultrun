// Package jev is an opt-in client for System One / Jev-compatible APIs:
// TypeSafe (api.typesafe.ai) and OpenJEV (api.openjev.sh), same /v1/systemone wire format.
// Used to gate agent completion claims against VaultRun verify evidence.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nickvd7/vaultrun/internal/httputil"
)

const (
	// DefaultBaseURL / DefaultModel are TypeSafe defaults (legacy aliases).
	DefaultBaseURL     = TypeSafeBaseURL
	DefaultModel       = TypeSafeModel
	DefaultTimeout     = 30 * time.Second
	MaxStateBytes      = 48 << 10 // 48 KiB — keep outbound payload bounded
	MaxClaimRunes      = 4000
	DefaultMinNoul     = 0.7
	DefaultMinComplete = 0.7
)

// Client talks to a Jev-compatible System One endpoint.
type Client struct {
	Provider Provider
	BaseURL  string
	APIKey   string
	Model    string
	HTTP     *http.Client
}

// ConfigFromEnv builds a client for TypeSafe or OpenJEV.
//
// Provider selection (JEV_PROVIDER):
//   - openjev — https://api.openjev.sh (OPENJEV_API_KEY, model openjev)
//   - typesafe — https://api.typesafe.ai (TYPESAFE_API_KEY, model jev-latest)
//
// If JEV_PROVIDER is unset and only OPENJEV_API_KEY is present, OpenJEV is used.
// Base URL must be public HTTPS unless JEV_ALLOW_PRIVATE_BASE=true (self-hosted OpenJev).
func ConfigFromEnv(getenv func(string) string) (*Client, error) {
	if getenv == nil {
		return nil, fmt.Errorf("getenv required")
	}
	provider := ResolveProvider(getenv)
	defs := defaultsFor(provider)

	key := firstEnv(getenv, defs.KeyEnvs...)
	if key == "" {
		return nil, fmt.Errorf("API key required — set OPENJEV_API_KEY (openjev.sh) or TYPESAFE_API_KEY")
	}

	base := strings.TrimRight(strings.TrimSpace(getenv(defs.BaseEnv)), "/")
	if base == "" {
		// Also accept the other provider's base env as explicit override.
		base = strings.TrimRight(strings.TrimSpace(getenv("JEV_BASE_URL")), "/")
	}
	if base == "" {
		base = defs.BaseURL
	}
	allowPrivate := getenv("JEV_ALLOW_PRIVATE_BASE") == "true"
	if err := validateBaseURL(base, allowPrivate); err != nil {
		return nil, fmt.Errorf("%s: %w", defs.BaseEnv, err)
	}

	model := strings.TrimSpace(getenv("JEV_MODEL"))
	if model == "" {
		model = defs.Model
	}

	return &Client{
		Provider: provider,
		BaseURL:  base,
		APIKey:   key,
		Model:    model,
		HTTP:     httpClientForBase(base, allowPrivate, DefaultTimeout),
	}, nil
}

// Question is one typed System One question.
type Question struct {
	Type         string            `json:"type"`
	Instructions any               `json:"instructions"`
	Criteria     any               `json:"criteria,omitempty"`
}

// Request is POST /v1/systemone.
type Request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

// Answer is one typed answer from Jev.
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

// Response is the System One API response.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   map[string]any    `json:"usage,omitempty"`
}

// Evaluate sends state + questions to Jev.
func (c *Client) Evaluate(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	if c == nil || c.APIKey == "" {
		return nil, fmt.Errorf("jev client not configured")
	}
	if len(questions) == 0 {
		return nil, fmt.Errorf("questions required")
	}
	model := c.Model
	if model == "" {
		model = DefaultModel
	}
	body := Request{State: state, Model: model, Questions: questions}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxStateBytes+16<<10 {
		return nil, fmt.Errorf("request payload too large (%d bytes)", len(raw))
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/v1/systemone"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vaultrun-jev/1.0")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = httputil.NoRedirectClient(DefaultTimeout)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jev request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("jev read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 256 {
			msg = msg[:256] + "…"
		}
		return nil, fmt.Errorf("jev API %d: %s", resp.StatusCode, msg)
	}
	var out Response
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("jev decode: %w", err)
	}
	return &out, nil
}

// SanitizeState truncates and redacts secrets before sending to Jev.
func SanitizeState(s string) string {
	s = RedactSecrets(s)
	if len(s) > MaxStateBytes {
		s = s[:MaxStateBytes] + "\n…[truncated]"
	}
	return s
}

// RedactSecrets strips common credential patterns from text sent off-host.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	replacements := []struct{ old, neu string }{
		// Keep simple substring redactionsuction for high-signal vault/API tokens.
	}
	_ = replacements
	out := s
	out = redactPrefixed(out, "vr_")
	out = redactPrefixed(out, "sk-")
	out = redactPrefixed(out, "ts_")
	out = redactPrefixed(out, "oj_")
	out = strings.ReplaceAll(out, "\x00", "")
	return out
}

func redactPrefixed(s, prefix string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], prefix) {
			j := i + len(prefix)
			for j < len(s) {
				c := s[j]
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
					j++
					continue
				}
				break
			}
			if j-i > len(prefix)+8 {
				b.WriteString(prefix)
				b.WriteString("[REDACTED]")
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ValidateClaim bounds claim length.
func ValidateClaim(claim string) error {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return fmt.Errorf("claim is required")
	}
	if utf8.RuneCountInString(claim) > MaxClaimRunes {
		return fmt.Errorf("claim too long (max %d runes)", MaxClaimRunes)
	}
	return nil
}
