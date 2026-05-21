package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/cleanup"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/policy"
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

	// Configure structured log level from LOG_LEVEL env (default: info).
	var logLevel slog.Level
	switch strings.ToLower(cfg.Observability.LogLevel) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

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

	// Load OPA policy if OPA_POLICY_FILE is configured; fall back to AllowAll.
	var policyHook policy.Hook = policy.AllowAll{}
	if policyFile := cfg.Auth.OPAPolicyFile; policyFile != "" {
		h, err := policy.NewOPAHookFromFile(context.Background(), policyFile)
		if err != nil {
			slog.Error("load opa policy", "file", policyFile, "err", err)
			os.Exit(1)
		}
		policyHook = h
		slog.Info("opa policy loaded", "file", policyFile)
	} else {
		// M-8: warn operators who start without an explicit policy — AllowAll
		// permits every command and every file path with no restrictions.
		slog.Warn("OPA_POLICY_FILE not set — running with AllowAll policy; " +
			"all commands and file paths are permitted inside sandbox containers")
	}

	rnr := runner.New(db, docker, al, policyHook)

	// Async run worker pool: 4 workers, buffer up to 256 pending jobs.
	asyncWorkers, _ := strconv.Atoi(getEnvMain("ASYNC_WORKERS", "4"))
	asyncBufSize, _ := strconv.Atoi(getEnvMain("ASYNC_QUEUE_SIZE", "256"))
	queue := jobqueue.New(rnr, asyncWorkers, asyncBufSize)

	// Start background cleanup of idle sessions.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	idleFor := time.Duration(cfg.Docker.IdleTimeoutMins) * time.Minute
	go cleanup.Start(cleanupCtx, db, docker, al, 5*time.Minute, idleFor)

	r := newRouter(cfg, db, docker, ws, rnr, al, policyHook, queue)

	srv := &http.Server{
		Addr:         cfg.ServerAddr(),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		if cfg.TLSEnabled() {
			slog.Info("vaultrun api starting (TLS)", "addr", cfg.ServerAddr(),
				"cert", cfg.Server.TLSCertFile)
			if err := srv.ListenAndServeTLS(cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
		} else {
			slog.Info("vaultrun api starting", "addr", cfg.ServerAddr())
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// Gracefully stop all running containers before exiting (when configured).
	if cfg.Observability.StopContainersOnShutdown {
		slog.Info("stopping active containers on shutdown")
		if sessions, err := dbpkg.ListActiveSessions(ctx, db); err != nil {
			slog.Error("shutdown: list active sessions", "err", err)
		} else {
			for _, s := range sessions {
				if s.ContainerID == nil {
					continue
				}
				slog.Info("shutdown: stopping container",
					"session_id", s.ID, "container_id", *s.ContainerID)
				if err := docker.StopSandbox(ctx, *s.ContainerID); err != nil {
					slog.Warn("shutdown: stop container failed",
						"session_id", s.ID, "err", err)
				}
			}
		}
	}

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "err", err)
	}
	fmt.Println("server stopped")
}

func getEnvMain(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
