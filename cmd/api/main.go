package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/cleanup"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

func main() {
	_ = godotenv.Load() // load .env if present; ignore missing file

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	db, err := dbpkg.Connect(cfg.Database)
	if err != nil {
		slog.Error("connect database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations (migrations directory relative to working dir)
	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "migrations"
	}
	if err := dbpkg.RunMigrations(db, migrationsPath); err != nil {
		slog.Error("run migrations", "err", err)
		os.Exit(1)
	}

	docker, err := dockerpkg.New()
	if err != nil {
		slog.Error("create docker client", "err", err)
		os.Exit(1)
	}

	ws := workspace.New(cfg.Workspace.BaseDir)

	// Ensure base workspace dir exists (0o700 = owner-only; no group/world read)
	if err := os.MkdirAll(cfg.Workspace.BaseDir, 0o700); err != nil {
		slog.Error("create workspace base dir", "err", err)
		os.Exit(1)
	}

	al := audit.New(db)
	rnr := runner.New(db, docker, al)

	// Start background cleanup of idle sessions.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	idleFor := time.Duration(cfg.Docker.IdleTimeoutMins) * time.Minute
	go cleanup.Start(cleanupCtx, db, docker, 5*time.Minute, idleFor)

	r := newRouter(cfg, db, docker, ws, rnr, al)

	srv := &http.Server{
		Addr:         cfg.ServerAddr(),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		slog.Info("vaultrun api starting", "addr", cfg.ServerAddr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "err", err)
	}
	fmt.Println("server stopped")
}
