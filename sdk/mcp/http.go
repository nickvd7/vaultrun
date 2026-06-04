// HTTP transport for the VaultRun MCP server.
// Activated by setting MCP_TRANSPORT=http.
//
// Environment variables (HTTP transport only):
//
//	MCP_PORT              Listen address (default: :8080, or :443 in ACME mode)
//	MCP_AUTH_TOKEN        Bearer token required on every request (required)
//	MCP_ACME_DOMAIN       Enable Let's Encrypt auto-TLS for this hostname
//	MCP_ACME_CACHE_DIR    Directory to cache ACME account keys and certs (default: /data/mcp-acme-cache)
//	MCP_ACME_EMAIL        Contact email for the ACME account (recommended; enables expiry notices)
//	MCP_TLS_CERT          Path to TLS certificate file (alternative to ACME)
//	MCP_TLS_KEY           Path to TLS private key file (alternative to ACME)
//	MCP_ALLOWED_ORIGINS   Comma-separated CORS origins (default: *)
//	MCP_TRUSTED_PROXIES   Comma-separated CIDRs/IPs of trusted reverse proxies.
//	                      When empty (default) X-Forwarded-For is NOT trusted and the
//	                      rate limiter keys on the real TCP peer address. Set this only
//	                      when running behind a known proxy, otherwise clients could
//	                      spoof their source IP to bypass per-IP rate limiting.
//	MCP_RATE_LIMIT        Max requests per minute per IP — global (default: 60)
//	MCP_RATE_LIMIT_WRITE  Additional per-IP limit for workspace-write tools like
//	                      upload_file and delete_file (default: MCP_RATE_LIMIT/2)
//	MCP_RATE_LIMIT_HEAVY  Additional per-IP limit for resource-creating tools like
//	                      run_command, create_session, create_snapshot, create_artifact
//	                      (default: MCP_RATE_LIMIT/3, minimum 1)
package main

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	acmeEmail      string
	tlsCert        string
	tlsKey         string
	allowedOrigins []string
	trustedProxies []string
	rateLimit      int
	writeTierLimit int
	heavyTierLimit int
}

// tlsActive reports whether the server terminates TLS itself (ACME or a static
// cert). Used to decide whether to emit HSTS.
func (c httpConfig) tlsActive() bool {
	return c.acmeDomain != "" || c.tlsCert != ""
}

// Server timeouts guard against slow-client (Slowloris) and zombie-connection
// resource exhaustion. They mirror the main VaultRun API server's posture.
const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 120 * time.Second
	httpIdleTimeout       = 90 * time.Second
	// Grace period for in-flight requests during graceful shutdown.
	httpShutdownTimeout = 30 * time.Second
)

// httpConfigFromEnv reads HTTP transport settings from environment variables.
// Returns an error if required variables are missing or configuration is invalid.
func httpConfigFromEnv() (httpConfig, error) {
	cfg := httpConfig{
		port:         getEnvOrDefault("MCP_PORT", ":8080"),
		authToken:    os.Getenv("MCP_AUTH_TOKEN"),
		acmeDomain:   os.Getenv("MCP_ACME_DOMAIN"),
		acmeCacheDir: getEnvOrDefault("MCP_ACME_CACHE_DIR", "/data/mcp-acme-cache"),
		acmeEmail:    os.Getenv("MCP_ACME_EMAIL"),
		tlsCert:      os.Getenv("MCP_TLS_CERT"),
		tlsKey:       os.Getenv("MCP_TLS_KEY"),
		rateLimit:    60,
	}
	if tp := os.Getenv("MCP_TRUSTED_PROXIES"); tp != "" {
		for _, p := range strings.Split(tp, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.trustedProxies = append(cfg.trustedProxies, p)
			}
		}
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
	// Tier limits default to fractions of the global limit; can be overridden.
	cfg.writeTierLimit = max(cfg.rateLimit/2, 1)
	cfg.heavyTierLimit = max(cfg.rateLimit/3, 1)
	if v := os.Getenv("MCP_RATE_LIMIT_WRITE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.writeTierLimit = n
		}
	}
	if v := os.Getenv("MCP_RATE_LIMIT_HEAVY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.heavyTierLimit = n
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

// ---------------------------------------------------------------------------
// Per-IP sliding-window rate limiter
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Per-tool rate-limit tiers
// ---------------------------------------------------------------------------

// heavyTools create containers, execute code, or create durable storage.
// They get the strictest per-IP limit on top of the global limit.
var heavyTools = map[string]bool{
	"run_command":     true,
	"create_session":  true,
	"create_snapshot": true,
	"create_artifact": true,
	"run_github_repo": true,
	"pull_image":      true,
	"lambda_invoke":   true, // executes arbitrary cloud workloads
}

// writeTools modify workspace state without spawning execution. They get a
// moderately stricter per-IP limit than read-only tools.
var writeTools = map[string]bool{
	"upload_file":          true,
	"delete_file":          true,
	"delete_session":       true,
	"github_post_comment":  true,
	"fs_write_file":        true,
	"fs_delete_file":       true,
	"s3_put_object":        true,
	"s3_delete_object":     true,
	"ssm_put_parameter":    true,
	"ssm_delete_parameter": true,
	"sm_get_secret":        true, // reading secrets is a sensitive write-tier op
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

// buildHTTPEngine constructs the Gin router with all middleware and routes.
// Extracted so tests can call it without binding to a real port.
//
// Middleware ordering (intentional):
//  1. Recovery     — panic → 500, never crash the server
//  2. CORS         — preflight OPTIONS is handled and aborted here
//  3. SecurityHdrs — applied to every response, including /healthz and 401s
//  4. RateLimit    — global per-IP flood protection, runs BEFORE auth so
//     brute-force token guesses are throttled too
//  5. /healthz     — unauthenticated readiness probe (registered before auth)
//  6. Auth         — bearer token required for all remaining routes
//  7. Routes       — /, /sse, /mcp (with per-tool tier limiting inside /mcp)
func buildHTTPEngine(srv *server, cfg httpConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// Trusted-proxy policy. Default: trust NO proxy, so c.ClientIP() returns
	// the real TCP peer address and a client cannot spoof X-Forwarded-For to
	// evade per-IP rate limiting. Opt in via MCP_TRUSTED_PROXIES.
	_ = r.SetTrustedProxies(cfg.trustedProxies)

	// CORS — must run before auth so that preflight requests from browsers get
	// a proper response without triggering the auth check.
	corsConfig := cors.DefaultConfig()
	if len(cfg.allowedOrigins) == 1 && cfg.allowedOrigins[0] == "*" {
		corsConfig.AllowAllOrigins = true
	} else {
		corsConfig.AllowOrigins = cfg.allowedOrigins
	}
	corsConfig.AllowMethods = []string{"GET", "POST", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Content-Type", "Authorization"}
	r.Use(cors.New(corsConfig))

	// Security headers on every response. We omit the legacy X-XSS-Protection
	// (deprecated; can introduce vulnerabilities in older browsers) and instead
	// rely on a strict CSP. HSTS is only emitted when this process terminates
	// TLS — sending it over plain HTTP is meaningless and sending it from
	// behind a TLS-terminating proxy is the proxy's responsibility.
	tlsActive := cfg.tlsActive()
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Header("Cache-Control", "no-store")
		if tlsActive {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	})

	// Global per-IP rate limiting runs BEFORE authentication so unauthenticated
	// floods and bearer-token brute-force attempts are throttled too.
	// (OPTIONS preflights pass through — the CORS middleware already handled them.)
	globalLimiter := newIPRateLimiter(cfg.rateLimit)
	r.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if !globalLimiter.allow(c.ClientIP()) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	})

	// GET /healthz — unauthenticated readiness probe for load balancers and
	// container orchestrators. Registered BEFORE the auth middleware so that
	// health checks from infrastructure do not require credentials. The probe
	// verifies the upstream VaultRun API is reachable; it does not leak any
	// sensitive information (only status + version + tool count).
	r.GET("/healthz", func(c *gin.Context) {
		if srv.client != nil {
			hctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
			defer cancel()
			if err := srv.client.HealthCheck(hctx); err != nil {
				slog.Warn("vaultrun-mcp: healthcheck failed", "err", err)
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"status": "degraded",
					"error":  "upstream VaultRun API unreachable",
				})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"status":      "ok",
			"version":     "0.1.0",
			"tools_count": len(toolDefinitions()),
		})
	})

	// Auth middleware — all routes registered below require a valid Bearer token.
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

	// Per-tool tier rate limiters. These are ADDITIONAL limits on top of the
	// global per-IP limit. Heavy tools (run_command, create_session, …) get the
	// strictest budget; write tools (upload_file, delete_file, …) get a moderate
	// budget; read-only tools are only subject to the global limit.
	// A tier limit of 0 means "disabled" — the global limit alone applies.
	var writeLimiter, heavyLimiter *ipRateLimiter
	if cfg.writeTierLimit > 0 {
		writeLimiter = newIPRateLimiter(cfg.writeTierLimit)
	}
	if cfg.heavyTierLimit > 0 {
		heavyLimiter = newIPRateLimiter(cfg.heavyTierLimit)
	}

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

		// Per-tool tier rate limiting for resource-intensive operations. We check
		// this after body decode (the tool name is inside the payload) but before
		// dispatching — a 429 is cheaper than spawning a container.
		if req.Method == "tools/call" && len(req.Params) > 0 {
			var params mcpToolCallParams
			if err := json.Unmarshal(req.Params, &params); err == nil {
				var tierLimiter *ipRateLimiter
				switch {
				case heavyTools[params.Name]:
					tierLimiter = heavyLimiter
				case writeTools[params.Name]:
					tierLimiter = writeLimiter
				}
				if tierLimiter != nil && !tierLimiter.allow(c.ClientIP()) {
					c.JSON(http.StatusTooManyRequests, gin.H{
						"error": "rate limit exceeded for this operation type",
					})
					return
				}
			}
		}

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

// ---------------------------------------------------------------------------
// Server lifecycle
// ---------------------------------------------------------------------------

// startHTTPServer starts the HTTP MCP server on the configured address.
// TLS priority: ACME > static cert/key > plain HTTP.
//
// All three modes use an explicit *http.Server with read/write/idle timeouts so
// a slow or idle client cannot tie up connections indefinitely (Slowloris).
//
// When ctx is cancelled (e.g. SIGINT/SIGTERM), in-flight requests are given
// httpShutdownTimeout to complete before the process exits cleanly.
func startHTTPServer(ctx context.Context, srv *server, cfg httpConfig) error {
	r := buildHTTPEngine(srv, cfg)

	newHTTPServer := func(addr string, tlsCfg *tls.Config) *http.Server {
		return &http.Server{
			Addr:              addr,
			Handler:           r,
			TLSConfig:         tlsCfg,
			ReadHeaderTimeout: httpReadHeaderTimeout,
			ReadTimeout:       httpReadTimeout,
			WriteTimeout:      httpWriteTimeout,
			IdleTimeout:       httpIdleTimeout,
		}
	}

	// gracefulStop waits for ctx to be cancelled then drains in-flight requests.
	gracefulStop := func(httpSrv *http.Server) {
		<-ctx.Done()
		slog.Info("vaultrun-mcp: shutdown signal received, draining requests...")
		shutCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			slog.Error("vaultrun-mcp: shutdown error", "err", err)
		}
	}

	if cfg.acmeDomain != "" {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.acmeDomain),
			Cache:      autocert.DirCache(cfg.acmeCacheDir),
			Email:      cfg.acmeEmail,
		}
		// HTTP-01 challenge handler on :80. Give it timeouts and a clean shutdown.
		challengeSrv := &http.Server{
			Addr:              ":80",
			Handler:           m.HTTPHandler(nil),
			ReadHeaderTimeout: httpReadHeaderTimeout,
		}
		go func() {
			<-ctx.Done()
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = challengeSrv.Shutdown(shutCtx)
		}()
		go func() {
			if err := challengeSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("vaultrun-mcp: ACME HTTP-01 handler stopped", "err", err)
			}
		}()

		httpsAddr := cfg.port
		if httpsAddr == ":8080" {
			httpsAddr = ":443"
		}
		httpsSrv := newHTTPServer(httpsAddr, &tls.Config{
			GetCertificate: m.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		})
		go gracefulStop(httpsSrv)
		slog.Info("vaultrun-mcp: ACME/Let's Encrypt listening",
			"addr", httpsAddr, "domain", cfg.acmeDomain, "cache", cfg.acmeCacheDir)
		if err := httpsSrv.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	if cfg.tlsCert != "" {
		httpsSrv := newHTTPServer(cfg.port, &tls.Config{MinVersion: tls.VersionTLS12})
		go gracefulStop(httpsSrv)
		slog.Info("vaultrun-mcp: TLS listening", "addr", cfg.port, "cert", cfg.tlsCert)
		if err := httpsSrv.ListenAndServeTLS(cfg.tlsCert, cfg.tlsKey); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	slog.Warn("vaultrun-mcp: HTTP listening WITHOUT TLS — only safe behind a TLS-terminating proxy or on localhost",
		"addr", cfg.port)
	plainSrv := newHTTPServer(cfg.port, nil)
	go gracefulStop(plainSrv)
	if err := plainSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Audit logging
// ---------------------------------------------------------------------------

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
