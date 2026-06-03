// HTTP transport for the VaultRun MCP server.
// Activated by setting MCP_TRANSPORT=http.
//
// Environment variables (HTTP transport only):
//
//	MCP_PORT             Listen address (default: :8080, or :443 in ACME mode)
//	MCP_AUTH_TOKEN       Bearer token required on every request (required)
//	MCP_ACME_DOMAIN      Enable Let's Encrypt auto-TLS for this hostname
//	MCP_ACME_CACHE_DIR   Directory to cache ACME account keys and certs (default: /data/mcp-acme-cache)
//	MCP_TLS_CERT         Path to TLS certificate file (alternative to ACME)
//	MCP_TLS_KEY          Path to TLS private key file (alternative to ACME)
//	MCP_ALLOWED_ORIGINS  Comma-separated CORS origins (default: *)
//	MCP_RATE_LIMIT       Max requests per minute per IP (default: 60)
package main

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/acme/autocert"
)

// httpConfig holds runtime configuration for the HTTP transport.
type httpConfig struct {
	port           string
	authToken      string
	acmeDomain     string
	acmeCacheDir   string
	tlsCert        string
	tlsKey         string
	allowedOrigins []string
	rateLimit      int
}

// httpConfigFromEnv reads HTTP transport settings from environment variables.
// Returns an error if required variables are missing or configuration is invalid.
func httpConfigFromEnv() (httpConfig, error) {
	cfg := httpConfig{
		port:         getEnvOrDefault("MCP_PORT", ":8080"),
		authToken:    os.Getenv("MCP_AUTH_TOKEN"),
		acmeDomain:   os.Getenv("MCP_ACME_DOMAIN"),
		acmeCacheDir: getEnvOrDefault("MCP_ACME_CACHE_DIR", "/data/mcp-acme-cache"),
		tlsCert:      os.Getenv("MCP_TLS_CERT"),
		tlsKey:       os.Getenv("MCP_TLS_KEY"),
		rateLimit:    60,
	}
	if cfg.authToken == "" {
		return httpConfig{}, fmt.Errorf("MCP_AUTH_TOKEN must be set when MCP_TRANSPORT=http")
	}
	if originsEnv := os.Getenv("MCP_ALLOWED_ORIGINS"); originsEnv != "" {
		cfg.allowedOrigins = strings.Split(originsEnv, ",")
	} else {
		cfg.allowedOrigins = []string{"*"}
	}
	if v := os.Getenv("MCP_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.rateLimit = n
		}
	}
	// ACME and static TLS are mutually exclusive.
	if cfg.acmeDomain != "" && (cfg.tlsCert != "" || cfg.tlsKey != "") {
		return httpConfig{}, fmt.Errorf("MCP_ACME_DOMAIN and MCP_TLS_CERT/MCP_TLS_KEY cannot be used together")
	}
	if cfg.tlsCert != "" || cfg.tlsKey != "" {
		if cfg.tlsCert == "" || cfg.tlsKey == "" {
			return httpConfig{}, fmt.Errorf("MCP_TLS_CERT and MCP_TLS_KEY must both be set")
		}
		if _, err := os.Stat(cfg.tlsCert); err != nil {
			return httpConfig{}, fmt.Errorf("MCP_TLS_CERT: %w", err)
		}
		if _, err := os.Stat(cfg.tlsKey); err != nil {
			return httpConfig{}, fmt.Errorf("MCP_TLS_KEY: %w", err)
		}
	}
	return cfg, nil
}

// sweepInterval is the number of allow() calls between full map sweeps that
// evict dormant, fully-expired IP entries. This bounds memory growth from
// one-shot or rotating source IPs (a cheap DoS vector) without a background
// goroutine or per-request O(n) scan.
const sweepInterval = 1024

// ipRateLimiter is a sliding-window rate limiter keyed by IP address.
type ipRateLimiter struct {
	mu        sync.Mutex
	windows   map[string][]time.Time
	limit     int
	sinceSwip int // allow() calls since the last sweep
}

func newIPRateLimiter(limit int) *ipRateLimiter {
	return &ipRateLimiter{windows: make(map[string][]time.Time), limit: limit}
}

// allow returns true if the request from ip should be allowed.
func (r *ipRateLimiter) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sinceSwip++; r.sinceSwip >= sweepInterval {
		r.sweepLocked()
		r.sinceSwip = 0
	}

	cutoff := time.Now().Add(-time.Minute)
	ts := r.windows[ip]
	n := 0
	for _, t := range ts {
		if t.After(cutoff) {
			ts[n] = t
			n++
		}
	}
	ts = ts[:n]
	if len(ts) >= r.limit {
		r.windows[ip] = ts
		return false
	}
	r.windows[ip] = append(ts, time.Now())
	return true
}

// sweepLocked drops IP entries whose timestamps are all outside the window.
// Caller must hold r.mu.
func (r *ipRateLimiter) sweepLocked() {
	cutoff := time.Now().Add(-time.Minute)
	for ip, ts := range r.windows {
		fresh := false
		for _, t := range ts {
			if t.After(cutoff) {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(r.windows, ip)
		}
	}
}

// buildHTTPEngine constructs the Gin router with all middleware and routes.
// Extracted so tests can call it without binding to a real port.
func buildHTTPEngine(srv *server, cfg httpConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// CORS
	corsConfig := cors.DefaultConfig()
	if len(cfg.allowedOrigins) == 1 && cfg.allowedOrigins[0] == "*" {
		corsConfig.AllowAllOrigins = true
	} else {
		corsConfig.AllowOrigins = cfg.allowedOrigins
	}
	corsConfig.AllowMethods = []string{"GET", "POST", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Content-Type", "Authorization"}
	r.Use(cors.New(corsConfig))

	// Security headers on every response.
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cache-Control", "no-store")
		c.Next()
	})

	// Bearer token authentication (OPTIONS preflight requests pass through).
	authToken := cfg.authToken
	r.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		header := c.GetHeader("Authorization")
		token := strings.TrimPrefix(header, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(authToken)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	})

	// Per-IP rate limiting.
	limiter := newIPRateLimiter(cfg.rateLimit)
	r.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if !limiter.allow(c.ClientIP()) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	})

	// GET / — server discovery info.
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"name":        "vaultrun-mcp",
			"version":     "0.1.0",
			"protocol":    "mcp/2024-11-05",
			"transport":   "http",
			"tools_count": len(toolDefinitions()),
		})
	})

	// GET /sse — Server-Sent Events stub for future push notifications.
	r.GET("/sse", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.String(http.StatusOK, ": connected\n\n")
	})

	// POST /mcp — main JSON-RPC 2.0 endpoint.
	r.POST("/mcp", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024*1024)

		var req jsonRPCRequest
		if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
			c.JSON(http.StatusOK, jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &jsonRPCError{Code: errParse, Message: "parse error: " + err.Error()},
			})
			return
		}

		slog.Debug("mcp/http: received", "method", req.Method, "id", req.ID, "ip", c.ClientIP())

		start := time.Now()
		resp := srv.handleRequest(c.Request.Context(), &req)

		if req.Method == "tools/call" {
			logHTTPToolCall(c, &req, resp, time.Since(start))
		}

		if resp == nil {
			c.Status(http.StatusNoContent)
			return
		}
		c.JSON(http.StatusOK, resp)
	})

	return r
}

// startHTTPServer starts the HTTP MCP server on the configured address.
// TLS priority: ACME > static cert/key > plain HTTP.
func startHTTPServer(srv *server, cfg httpConfig) error {
	r := buildHTTPEngine(srv, cfg)

	if cfg.acmeDomain != "" {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.acmeDomain),
			Cache:      autocert.DirCache(cfg.acmeCacheDir),
		}
		// HTTP-01 challenge handler on :80.
		go func() {
			if err := http.ListenAndServe(":80", m.HTTPHandler(nil)); err != nil {
				slog.Error("vaultrun-mcp: ACME HTTP-01 handler stopped", "err", err)
			}
		}()
		// HTTPS on :443 (or MCP_PORT if explicitly overridden).
		httpsAddr := cfg.port
		if httpsAddr == ":8080" {
			httpsAddr = ":443"
		}
		httpsSrv := &http.Server{
			Addr:      httpsAddr,
			Handler:   r,
			TLSConfig: &tls.Config{GetCertificate: m.GetCertificate, MinVersion: tls.VersionTLS12},
		}
		slog.Info("vaultrun-mcp: ACME/Let's Encrypt listening", "addr", httpsAddr, "domain", cfg.acmeDomain, "cache", cfg.acmeCacheDir)
		return httpsSrv.ListenAndServeTLS("", "")
	}

	if cfg.tlsCert != "" {
		slog.Info("vaultrun-mcp: TLS listening", "addr", cfg.port, "cert", cfg.tlsCert)
		return r.RunTLS(cfg.port, cfg.tlsCert, cfg.tlsKey)
	}

	slog.Info("vaultrun-mcp: HTTP listening (no TLS)", "addr", cfg.port)
	return r.Run(cfg.port)
}

// logHTTPToolCall logs a tools/call request with sensitive parameters redacted.
func logHTTPToolCall(c *gin.Context, req *jsonRPCRequest, resp *jsonRPCResponse, dur time.Duration) {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return
	}

	args := make(map[string]any)
	if len(params.Arguments) > 0 {
		_ = json.Unmarshal(params.Arguments, &args)
	}
	// Redact fields that may contain secrets or large content.
	for _, key := range []string{"env", "content", "secret_env"} {
		if _, ok := args[key]; ok {
			args[key] = "[REDACTED]"
		}
	}

	isError := false
	if resp != nil && resp.Result != nil {
		if b, err := json.Marshal(resp.Result); err == nil {
			var tr mcpToolResult
			if err := json.Unmarshal(b, &tr); err == nil {
				isError = tr.IsError
			}
		}
	}

	slog.Info("mcp_tool_call",
		"tool", params.Name,
		"client_ip", c.ClientIP(),
		"duration_ms", dur.Milliseconds(),
		"is_error", isError,
		"args", args,
	)
}
