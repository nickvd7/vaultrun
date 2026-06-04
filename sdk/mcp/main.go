// VaultRun MCP Server — exposes VaultRun sandbox capabilities as MCP tools.
//
// Usage (stdio transport — default, for Claude Desktop/Code):
//
//	VAULTRUN_BASE_URL=http://localhost:8080 \
//	VAULTRUN_API_KEY=vr_yourkeyhere \
//	./vaultrun-mcp
//
// Usage (HTTP transport — for OpenAI, OpenRouter, and other platforms):
//
//	MCP_TRANSPORT=http \
//	MCP_AUTH_TOKEN=your-secret-token \
//	VAULTRUN_BASE_URL=http://localhost:8080 \
//	VAULTRUN_API_KEY=vr_yourkeyhere \
//	./vaultrun-mcp
//
// Environment variables (all transports):
//
//	VAULTRUN_BASE_URL        Base URL of the VaultRun API (required)
//	VAULTRUN_API_KEY         API key for authentication (required)
//	VAULTRUN_DEFAULT_IMAGE   Default Docker image for sessions (default: python:3.12-slim)
//	VAULTRUN_LOG_FILE        Write server logs to this file instead of stderr
//	MCP_TRANSPORT            Transport to use: "stdio" (default) or "http"
//	GITHUB_TOKEN             GitHub personal access token (optional). Required for
//	                         run_github_repo to clone private repos and for
//	                         github_post_comment to post to repos.
//	MCP_FS_ALLOWED_PATHS     Comma-separated list of absolute paths the filesystem
//	                         tools (fs_read_file, fs_write_file, fs_list_dir,
//	                         fs_delete_file) are allowed to access. When unset,
//	                         all filesystem tools return an error.
//
// Additional environment variables for MCP_TRANSPORT=http (see http.go):
//
//	MCP_AUTH_TOKEN, MCP_PORT, MCP_ALLOWED_ORIGINS, MCP_RATE_LIMIT
//	MCP_TRUSTED_PROXIES                  (CIDRs/IPs of trusted reverse proxies)
//	MCP_ACME_DOMAIN, MCP_ACME_CACHE_DIR, MCP_ACME_EMAIL  (Let's Encrypt auto-TLS)
//	MCP_TLS_CERT, MCP_TLS_KEY            (static cert, alternative to ACME)
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	// Configure logging: default to stderr, redirect to file when VAULTRUN_LOG_FILE is set.
	logWriter := os.Stderr
	if lf := os.Getenv("VAULTRUN_LOG_FILE"); lf != "" {
		f, err := os.OpenFile(lf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vaultrun-mcp: open log file %q: %v\n", lf, err)
			os.Exit(1)
		}
		defer f.Close()
		logWriter = f
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelInfo})))

	baseURL := strings.TrimRight(os.Getenv("VAULTRUN_BASE_URL"), "/")
	apiKey := os.Getenv("VAULTRUN_API_KEY")
	defaultImage := getEnvOrDefault("VAULTRUN_DEFAULT_IMAGE", "python:3.12-slim")
	githubToken := os.Getenv("GITHUB_TOKEN")
	fs := loadFSConfig()

	if baseURL == "" || apiKey == "" {
		slog.Error("VAULTRUN_BASE_URL and VAULTRUN_API_KEY must be set")
		os.Exit(1)
	}

	// Graceful shutdown: cancel ctx on SIGINT or SIGTERM. The HTTP transport
	// listens on ctx.Done() and drains in-flight requests before exiting.
	// The stdio transport's serve() loop exits naturally when stdin closes.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := newVaultRunClient(baseURL, apiKey)
	srv := newServer(client, defaultImage, githubToken, fs)

	switch os.Getenv("MCP_TRANSPORT") {
	case "http":
		cfg, err := httpConfigFromEnv()
		if err != nil {
			slog.Error("vaultrun-mcp: invalid HTTP config", "err", err)
			os.Exit(1)
		}
		if err := startHTTPServer(ctx, srv, cfg); err != nil {
			slog.Error("vaultrun-mcp: HTTP server error", "err", err)
			os.Exit(1)
		}
	default:
		slog.Info("vaultrun-mcp: starting stdio transport", "base_url", baseURL)
		if err := srv.serve(ctx, os.Stdin, os.Stdout); err != nil && err != io.EOF {
			slog.Error("vaultrun-mcp: fatal", "err", err)
			os.Exit(1)
		}
		slog.Info("vaultrun-mcp: stopped")
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
