// Package localgateway implements an OpenAI-compatible action gateway for local
// inference engines (Ollama, LM Studio, vLLM, …). It proxies chat completions to
// an operator-configured upstream and executes VaultRun sandbox tools for any
// tool_calls the model returns.
package localgateway

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime settings for the local inference gateway.
type Config struct {
	// ListenAddr is the HTTP bind address (e.g. ":8091").
	ListenAddr string
	// AuthToken is the required Bearer token for /v1/* (required, min 16 chars).
	AuthToken string
	// UpstreamURL is the OpenAI-compatible base URL (e.g. http://127.0.0.1:11434).
	// Only operator-configured; never taken from request bodies.
	UpstreamURL string
	// UpstreamTimeout bounds a single upstream chat request.
	UpstreamTimeout time.Duration
	// VaultRunBaseURL and VaultRunAPIKey authenticate to the VaultRun API.
	VaultRunBaseURL string
	VaultRunAPIKey  string
	// DefaultImage is used when auto-creating sessions.
	DefaultImage string
	// NetworkEnabled controls whether auto-created sessions get network.
	NetworkEnabled bool
	// MaxToolLoops caps model→tool→model iterations per request.
	MaxToolLoops int
	// MaxBodyBytes limits inbound JSON body size.
	MaxBodyBytes int64
	// MaxToolResultBytes truncates tool stdout/stderr fed back to the model.
	MaxToolResultBytes int
	// MaxFileBytes caps write_file content size.
	MaxFileBytes int
	// RateLimitPerMin is the per-IP request budget (fixed 1-minute window).
	RateLimitPerMin int
	// DefaultModel is injected when the client omits "model".
	DefaultModel string
	// RunTimeoutSeconds default for vaultrun_run_command when the model omits it.
	RunTimeoutSeconds int
	// MaxRunTimeoutSeconds hard cap for per-run timeouts.
	MaxRunTimeoutSeconds int
	// CaptureMissions saves successful VaultRun tool sequences as missions.
	CaptureMissions bool
}

// LoadConfigFromEnv reads configuration from environment variables.
func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		// Default to loopback only — sandbox-capable API must not be LAN-exposed
		// unless the operator explicitly binds a non-loopback address.
		ListenAddr:           envOr("LOCAL_GATEWAY_PORT", "127.0.0.1:8091"),
		AuthToken:            os.Getenv("LOCAL_GATEWAY_AUTH_TOKEN"),
		UpstreamURL:          strings.TrimRight(envOr("LOCAL_GATEWAY_UPSTREAM_URL", "http://127.0.0.1:11434"), "/"),
		UpstreamTimeout:      time.Duration(envInt("LOCAL_GATEWAY_UPSTREAM_TIMEOUT_SEC", 120)) * time.Second,
		VaultRunBaseURL:      strings.TrimRight(os.Getenv("VAULTRUN_BASE_URL"), "/"),
		VaultRunAPIKey:       firstNonEmpty(os.Getenv("VAULTRUN_API_KEY"), os.Getenv("LOCAL_GATEWAY_VAULTRUN_KEY")),
		DefaultImage:         envOr("LOCAL_GATEWAY_DEFAULT_IMAGE", "python:3.12-slim"),
		NetworkEnabled:       envOr("LOCAL_GATEWAY_NETWORK_ENABLED", "false") == "true",
		MaxToolLoops:         envInt("LOCAL_GATEWAY_MAX_TOOL_LOOPS", 8),
		MaxBodyBytes:         int64(envInt("LOCAL_GATEWAY_MAX_BODY_BYTES", 1<<20)), // 1 MiB
		MaxToolResultBytes:   envInt("LOCAL_GATEWAY_MAX_TOOL_RESULT_BYTES", 32<<10),
		MaxFileBytes:         envInt("LOCAL_GATEWAY_MAX_FILE_BYTES", 256<<10),
		RateLimitPerMin:      envInt("LOCAL_GATEWAY_RATE_LIMIT", 60),
		DefaultModel:         os.Getenv("LOCAL_GATEWAY_DEFAULT_MODEL"),
		RunTimeoutSeconds:    envInt("LOCAL_GATEWAY_RUN_TIMEOUT_SEC", 60),
		MaxRunTimeoutSeconds: envInt("LOCAL_GATEWAY_MAX_RUN_TIMEOUT_SEC", 300),
		// Default on — local AI workflows become owned assets. Disable with =false.
		CaptureMissions: envOr("LOCAL_GATEWAY_CAPTURE_MISSIONS", "true") != "false",
	}
	cfg.ListenAddr = normalizeListenAddr(cfg.ListenAddr)
	return cfg, cfg.Validate()
}

// normalizeListenAddr accepts ":8091", "8091", or "host:port".
// A bare port becomes 127.0.0.1:<port> (loopback), not 0.0.0.0.
func normalizeListenAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "127.0.0.1:8091"
	}
	// Bare port → loopback.
	if !strings.Contains(addr, ":") {
		return "127.0.0.1:" + addr
	}
	// ":8091" → all interfaces (explicit operator choice via leading colon).
	// Keep as-is so operators who want LAN bind can set LOCAL_GATEWAY_PORT=:8091.
	return addr
}

// ListenAddrExposesNonLoopback reports whether the listen address is likely
// reachable from outside the host (used for a startup warning).
func ListenAddrExposesNonLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// ":8091" form — SplitHostPort needs a host; try with placeholder.
		if strings.HasPrefix(addr, ":") {
			return true
		}
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Hostname — treat as potentially non-loopback.
		return !strings.EqualFold(host, "localhost")
	}
	return !ip.IsLoopback()
}

// Validate checks required fields and security-sensitive bounds.
func (c Config) Validate() error {
	if c.AuthToken == "" {
		return fmt.Errorf("LOCAL_GATEWAY_AUTH_TOKEN is required")
	}
	if len(c.AuthToken) < 16 {
		return fmt.Errorf("LOCAL_GATEWAY_AUTH_TOKEN must be at least 16 characters")
	}
	if c.VaultRunBaseURL == "" {
		return fmt.Errorf("VAULTRUN_BASE_URL is required")
	}
	if c.VaultRunAPIKey == "" {
		return fmt.Errorf("VAULTRUN_API_KEY (or LOCAL_GATEWAY_VAULTRUN_KEY) is required")
	}
	if err := validateUpstreamURL(c.UpstreamURL); err != nil {
		return fmt.Errorf("LOCAL_GATEWAY_UPSTREAM_URL: %w", err)
	}
	if c.MaxToolLoops < 1 || c.MaxToolLoops > 32 {
		return fmt.Errorf("LOCAL_GATEWAY_MAX_TOOL_LOOPS must be between 1 and 32")
	}
	if c.MaxBodyBytes < 1024 || c.MaxBodyBytes > 16<<20 {
		return fmt.Errorf("LOCAL_GATEWAY_MAX_BODY_BYTES must be between 1KiB and 16MiB")
	}
	if c.RateLimitPerMin < 1 {
		return fmt.Errorf("LOCAL_GATEWAY_RATE_LIMIT must be >= 1")
	}
	if c.RunTimeoutSeconds < 1 || c.RunTimeoutSeconds > c.MaxRunTimeoutSeconds {
		return fmt.Errorf("LOCAL_GATEWAY_RUN_TIMEOUT_SEC must be between 1 and MAX_RUN_TIMEOUT_SEC")
	}
	if c.MaxRunTimeoutSeconds < 1 || c.MaxRunTimeoutSeconds > 3600 {
		return fmt.Errorf("LOCAL_GATEWAY_MAX_RUN_TIMEOUT_SEC must be between 1 and 3600")
	}
	if c.UpstreamTimeout < time.Second || c.UpstreamTimeout > 10*time.Minute {
		return fmt.Errorf("LOCAL_GATEWAY_UPSTREAM_TIMEOUT_SEC must be between 1s and 10m")
	}
	if c.DefaultImage == "" {
		return fmt.Errorf("LOCAL_GATEWAY_DEFAULT_IMAGE must not be empty")
	}
	return nil
}

// validateUpstreamURL ensures the operator-configured upstream is a sane http(s)
// URL with a host. Private/loopback hosts are intentionally allowed (Ollama on
// 127.0.0.1). User-controlled request bodies never override this URL.
func validateUpstreamURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	// Reject credentials in the URL — use a dedicated proxy if auth is needed.
	if u.User != nil {
		return fmt.Errorf("userinfo in upstream URL is not allowed")
	}
	// Reject obviously dangerous schemes / hosts that look like metadata names
	// even when they resolve publicly (defense in depth; dial still goes to the
	// configured host only).
	blocked := map[string]bool{
		"metadata.google.internal": true,
		"metadata":                 true,
	}
	if blocked[strings.ToLower(host)] {
		return fmt.Errorf("host %q is not allowed as upstream", host)
	}
	// If the host is a literal IP, accept any (including private) — local AI.
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
