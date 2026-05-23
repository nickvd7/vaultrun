// VaultRun MCP Server — exposes VaultRun sandbox capabilities as MCP tools.
//
// Usage:
//
//	VAULTRUN_BASE_URL=http://localhost:8080 \
//	VAULTRUN_API_KEY=vr_yourkeyhere \
//	./vaultrun-mcp
//
// The server speaks the Model Context Protocol (MCP) over stdin/stdout using
// JSON-RPC 2.0. Any MCP-compatible host (Claude Desktop, Claude Code, etc.)
// can connect to it.
//
// Environment variables:
//
//	VAULTRUN_BASE_URL        Base URL of the VaultRun API (required)
//	VAULTRUN_API_KEY         API key for authentication (required)
//	VAULTRUN_DEFAULT_IMAGE   Default Docker image for sessions (default: python:3.12-slim)
//	VAULTRUN_LOG_FILE        Write server logs to this file instead of stderr
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func main() {
	// Configure logging: default to stderr, redirect to file when VAULTRUN_LOG_FILE is set.
	// (We can't log to stderr normally because the MCP host reads stdout/stdin and anything
	// on stderr goes to the host's log, but that's actually fine for debugging.)
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
	defaultImage := os.Getenv("VAULTRUN_DEFAULT_IMAGE")
	if defaultImage == "" {
		defaultImage = "python:3.12-slim"
	}

	if baseURL == "" || apiKey == "" {
		slog.Error("VAULTRUN_BASE_URL and VAULTRUN_API_KEY must be set")
		os.Exit(1)
	}

	client := newVaultRunClient(baseURL, apiKey)
	srv := newServer(client, defaultImage)

	slog.Info("vaultrun-mcp: starting", "base_url", baseURL)
	if err := srv.serve(context.Background(), os.Stdin, os.Stdout); err != nil && err != io.EOF {
		slog.Error("vaultrun-mcp: fatal", "err", err)
		os.Exit(1)
	}
	slog.Info("vaultrun-mcp: stopped")
}
