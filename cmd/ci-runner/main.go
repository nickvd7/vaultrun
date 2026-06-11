// VaultRun CI Runner — GitHub webhook receiver that runs PR test suites
// in isolated VaultRun sandboxes and posts results back to the pull request.
//
// Usage:
//
//	GITHUB_TOKEN=ghp_...               \
//	GITHUB_WEBHOOK_SECRET=your-secret  \
//	VAULTRUN_BASE_URL=http://vaultrun  \
//	VAULTRUN_API_KEY=vr_...            \
//	./ci-runner
//
// Environment variables:
//
//	GITHUB_TOKEN            GitHub PAT with repo + write:discussion scopes (required)
//	GITHUB_WEBHOOK_SECRET   Secret configured in the GitHub webhook settings (required)
//	VAULTRUN_BASE_URL       Base URL of the VaultRun API (required)
//	VAULTRUN_API_KEY        API key for the VaultRun API (required)
//	CI_DOCKER_IMAGE         Docker image for the sandbox (default: ubuntu:22.04)
//	CI_TEST_COMMANDS        JSON array of command arrays to run after cloning,
//	                        e.g. '[["go","test","./..."],["go","vet","./..."]]'
//	                        (default: [["make","test"]])
//	PORT                    HTTP listen address (default: :8080)
//	SLACK_WEBHOOK_URL       Slack Incoming Webhook URL (optional)
//	TEAMS_WEBHOOK_URL       Microsoft Teams Workflows webhook URL (optional)
//	NOTIFY_ON_SUCCESS       Set to "false" to suppress success notifications
//	                        and only alert on failures (default: true)
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := loadConfig()
	if err != nil {
		slog.Error("ci-runner: bad configuration", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/webhook", &webhookHandler{cfg: cfg})
	mux.HandleFunc("/healthz", healthzHandler)

	srv := &http.Server{
		Addr:         cfg.port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("ci-runner: listening for GitHub webhooks", "addr", cfg.port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("ci-runner: server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("ci-runner: shutting down, waiting for active CI runs…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("ci-runner: forced shutdown", "err", err)
	}
}
