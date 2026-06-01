package main

import (
	"context"
	"crypto/tls"
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
	"golang.org/x/crypto/acme/autocert"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/cleanup"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/metrics"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/secrets"
	"github.com/nickvd7/vaultrun/internal/siemexport"
	"github.com/nickvd7/vaultrun/internal/warmpool"
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

	// Startup pre-flight checks — fail fast so operators see problems immediately
	// rather than discovering them at runtime on the first real request.

	// MASTER_API_KEY: without this, no API keys can be created and the system is
	// inaccessible. Warn loudly; do not exit (key may have been intentionally
	// cleared after bootstrapping if keys already exist in the DB).
	if cfg.Auth.MasterKey == "" {
		slog.Warn("MASTER_API_KEY is not set — POST /api/v1/keys will be inaccessible; " +
			"set the key to create new API keys or verify existing keys are present in the database")
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

	// Mark runs that were still "pending" at previous shutdown as "failed".
	// These were in-flight when the server was killed and will never complete.
	// For Redis-backed async runs the reaper goroutine will re-deliver them;
	// this is a safety net that covers both queue backends and the in-memory case.
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if n, err := dbpkg.FailStalePendingRuns(startupCtx, db, time.Now().UTC()); err != nil {
		slog.Error("startup: fail stale pending runs", "err", err)
	} else if n > 0 {
		slog.Warn("startup: marked stale pending runs as failed", "count", n)
	}
	startupCancel()

	docker, err := dockerpkg.New()
	if err != nil {
		slog.Error("create docker client", "err", err)
		os.Exit(1)
	}
	// Verify the Docker daemon is reachable before accepting traffic. A
	// misconfigured DOCKER_HOST causes every session creation to fail; better to
	// surface it at startup.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := docker.Inner().Ping(pingCtx); err != nil {
		slog.Error("docker daemon unreachable — check DOCKER_HOST",
			"addr", cfg.Docker.Host, "err", err)
		pingCancel()
		os.Exit(1)
	}
	pingCancel()
	slog.Info("docker daemon reachable", "host", cfg.Docker.Host)

	ws := workspace.New(cfg.Workspace.BaseDir)

	// Ensure base workspace dir exists (0o700 = owner-only; no group/world read)
	if err := os.MkdirAll(cfg.Workspace.BaseDir, 0o700); err != nil {
		slog.Error("create workspace base dir", "err", err)
		os.Exit(1)
	}
	// Verify the workspace directory is actually writable. A read-only mount or
	// wrong ownership would cause silent failures at session creation time.
	probeFile := fmt.Sprintf("%s/.vaultrun-startup-probe", cfg.Workspace.BaseDir)
	if err := os.WriteFile(probeFile, []byte("ok"), 0o600); err != nil {
		slog.Error("workspace base dir is not writable — check WORKSPACE_BASE_DIR permissions",
			"dir", cfg.Workspace.BaseDir, "err", err)
		os.Exit(1)
	}
	_ = os.Remove(probeFile)

	al := audit.New(db)

	// Initialise secrets provider (env / Vault / AWS based on SECRETS_PROVIDER).
	sec := secrets.New()
	slog.Info("secrets provider initialised", "provider", sec.Name())

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

	// Warm container pool — optional; disabled when WARM_POOL_SIZE=0 or WARM_POOL_IMAGE is empty.
	var pool *warmpool.Pool
	poolCtx, poolCancel := context.WithCancel(context.Background())
	defer poolCancel()
	if cfg.Docker.WarmPoolSize > 0 && cfg.Docker.WarmPoolImage != "" {
		// Pre-pull the image at startup so the first Acquire() is instant.
		// Without this the pool fill goroutine would block on a pull the first time
		// it tries to create a container, causing pool starvation under early load.
		pullCtx, pullCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		exists, _ := docker.ImageExists(pullCtx, cfg.Docker.WarmPoolImage)
		if !exists {
			slog.Info("warm pool: pulling image", "image", cfg.Docker.WarmPoolImage)
			if err := docker.PullImage(pullCtx, cfg.Docker.WarmPoolImage); err != nil {
				// Non-fatal: the pool fill goroutine will retry; log and continue.
				slog.Warn("warm pool: pre-pull failed — pool will retry in background",
					"image", cfg.Docker.WarmPoolImage, "err", err)
			} else {
				slog.Info("warm pool: image ready", "image", cfg.Docker.WarmPoolImage)
			}
		}
		pullCancel()

		slog.Info("warm container pool enabled",
			"image", cfg.Docker.WarmPoolImage,
			"size", cfg.Docker.WarmPoolSize)
		pool = warmpool.New(docker, cfg.Docker.WarmPoolImage,
			cfg.Docker.WarmPoolSize, cfg.Workspace.BaseDir)
		pool.Start(poolCtx)
	}

	// Async run worker pool.
	// When REDIS_ADDR is set, use the durable Redis Streams backend.
	// Otherwise fall back to the in-process bounded channel (jobs lost on restart).
	asyncWorkers, _ := strconv.Atoi(getEnvMain("ASYNC_WORKERS", "4"))
	asyncBufSize, _ := strconv.Atoi(getEnvMain("ASYNC_QUEUE_SIZE", "256"))

	// Log whether rate limiting is backed by Redis or in-memory.
	if cfg.Redis.Addr != "" {
		slog.Info("rate limiter: using Redis backend (distributed, shared across instances)",
			"addr", cfg.Redis.Addr)
	} else {
		slog.Warn("rate limiter: using in-memory backend (process-local; set REDIS_ADDR for distributed rate limiting)")
	}

	var queue jobqueue.Queue
	if cfg.Redis.Addr != "" {
		q, err := jobqueue.NewRedis(
			rnr, db,
			cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB,
			asyncWorkers, cfg.Observability.WebhookSecret,
		)
		if err != nil {
			slog.Error("create redis job queue — check Redis connectivity or clear REDIS_ADDR to use in-memory queue",
				"addr", cfg.Redis.Addr, "err", err)
			os.Exit(1)
		}
		queue = q
	} else {
		slog.Info("async job queue: using in-memory (set REDIS_ADDR for durable queue)")
		queue = jobqueue.New(rnr, asyncWorkers, asyncBufSize, cfg.Observability.WebhookSecret)
	}

	// Background goroutine: publish job queue depth to Prometheus every 15s.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			metrics.JobQueueDepth.Set(float64(queue.Len()))
		}
	}()

	// Start background cleanup of idle sessions.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	idleFor := time.Duration(cfg.Docker.IdleTimeoutMins) * time.Minute
	go cleanup.Start(cleanupCtx, db, docker, al, 5*time.Minute, idleFor, cfg.Observability.AuditLogRetentionDays)

	// SIEM audit export — optional; only active when AUDIT_EXPORT_URL is set.
	if siemExp := siemexport.New(db); siemExp != nil {
		siemExp.Start(cleanupCtx)
		slog.Info("siem audit export started", "url", os.Getenv("AUDIT_EXPORT_URL"))
	}

	r := newRouter(cfg, db, docker, ws, rnr, al, policyHook, queue, sec, pool)

	srv := &http.Server{
		Addr:         cfg.ServerAddr(),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		switch {
		case cfg.ACMEEnabled():
			// ACME / Let's Encrypt: auto-obtain and renew certificate.
			// The cache dir persists account keys + certificates across restarts.
			if err := os.MkdirAll(cfg.Server.ACMECacheDir, 0o700); err != nil {
				slog.Error("create acme cache dir", "dir", cfg.Server.ACMECacheDir, "err", err)
				os.Exit(1)
			}
			m := &autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(cfg.Server.ACMEDomain),
				Cache:      autocert.DirCache(cfg.Server.ACMECacheDir),
			}
			// HTTP-01 challenge listener on :80 (background goroutine).
			go func() {
				if err := http.ListenAndServe(":80", m.HTTPHandler(nil)); err != nil {
					slog.Warn("acme http-01 listener stopped", "err", err)
				}
			}()
			srv.Addr = ":443"
			tc := m.TLSConfig()
			tc.MinVersion = tls.VersionTLS12
			srv.TLSConfig = tc
			slog.Info("vaultrun api starting (ACME/Let's Encrypt)",
				"domain", cfg.Server.ACMEDomain, "cache", cfg.Server.ACMECacheDir)
			if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
		case cfg.TLSEnabled():
			srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			slog.Info("vaultrun api starting (TLS)", "addr", cfg.ServerAddr(),
				"cert", cfg.Server.TLSCertFile)
			if err := srv.ListenAndServeTLS(cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
		default:
			slog.Warn("vaultrun api starting without TLS — API keys are transmitted in plaintext; " +
				"configure TLS_CERT_FILE/TLS_KEY_FILE or ACME_DOMAIN before exposing to untrusted networks")
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

	// Drain the async job queue: stop accepting new jobs and wait for in-flight
	// runs to complete (bounded by the shutdown timeout). This ensures no run
	// is silently abandoned mid-execution on SIGTERM.
	slog.Info("draining async job queue")
	queue.Drain(ctx)

	if pool != nil {
		slog.Info("draining warm container pool")
		pool.Stop()
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
