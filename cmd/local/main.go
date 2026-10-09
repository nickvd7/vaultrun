// VaultRun Local Gateway — OpenAI-compatible action plane for local inference.
//
// Proxies /v1/chat/completions to Ollama / LM Studio / vLLM and executes
// VaultRun sandbox tools whenever the model returns tool_calls.
//
// Usage:
//
//	LOCAL_GATEWAY_AUTH_TOKEN=... \
//	VAULTRUN_BASE_URL=http://localhost:8080 \
//	VAULTRUN_API_KEY=vr_... \
//	LOCAL_GATEWAY_UPSTREAM_URL=http://127.0.0.1:11434 \
//	./vaultrun-local
//
// See docs/local-gateway.md.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nickvd7/vaultrun/internal/localgateway"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := localgateway.LoadConfigFromEnv()
	if err != nil {
		slog.Error("local-gateway: bad configuration", "err", err)
		os.Exit(1)
	}

	gw := localgateway.NewFromConfig(cfg)
	srv := gw.Server()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("local-gateway: listening",
			"addr", cfg.ListenAddr,
			"upstream", cfg.UpstreamURL,
			"vaultrun", cfg.VaultRunBaseURL,
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("local-gateway: server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("local-gateway: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("local-gateway: forced shutdown", "err", err)
	}
}
