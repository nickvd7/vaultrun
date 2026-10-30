package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/acme/autocert"

	"github.com/redis/go-redis/v9"

	"github.com/nickvd7/vaultrun/internal/artifacts"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/cleanup"
	"github.com/nickvd7/vaultrun/internal/collab"
	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/cost"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/dockerguard"
	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/metrics"
	"github.com/nickvd7/vaultrun/internal/missions"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/quota"
	"github.com/nickvd7/vaultrun/internal/replay"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/secrets"
	"github.com/nickvd7/vaultrun/internal/siemexport"
	"github.com/nickvd7/vaultrun/internal/supervise"
	"github.com/nickvd7/vaultrun/internal/templates"
	"github.com/nickvd7/vaultrun/internal/version"
	"github.com/nickvd7/vaultrun/internal/warmpool"
	"github.com/nickvd7/vaultrun/internal/workspace"
	"github.com/nickvd7/vaultrun/internal/xfsquota"
)

func main() {
	// Installed into every sandbox as /.vaultrun/vr-exec, this binary runs
	// commands there under a timeout it enforces (see internal/supervise).
	if filepath.Base(os.Args[0]) == supervise.Name {
		// The same binary sets XFS workspace quotas from a privileged
		// one-shot container (WORKSPACE_QUOTA=xfs).
		if len(os.Args) > 1 && os.Args[1] == "xfs-quota" {
			os.Exit(xfsquota.Main(os.Args[2:]))
		}
		os.Exit(supervise.Main(os.Args[1:]))
	}
	// The Docker guard: the only process with the Docker socket (see
	// internal/dockerguard and the docker-guard service in compose).
	if len(os.Args) > 1 && os.Args[1] == "docker-guard" {
		os.Exit(dockerguard.Main(os.Args[2:]))
	}
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version" || os.Args[1] == "version") {
		fmt.Println("vaultrun-api", version.String())
		return
	}
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
	slog.Info("vaultrun api starting", "version", version.Version(), "revision", version.Revision())

	// Startup pre-flight checks — fail fast so operators see problems immediately
	// rather than discovering them at runtime on the first real request.

	// MASTER_API_KEY: without this, no API keys can be created and the system is
	// inaccessible. Warn loudly; do not exit (key may have been intentionally
	// cleared after bootstrapping if keys already exist in the DB).
	if cfg.Auth.MasterKey == "" {
		slog.Warn("MASTER_API_KEY is not set — POST /api/v1/keys will be inaccessible; " +
			"set the key to create new API keys or verify existing keys are present in the database")
	} else if len(cfg.Auth.MasterKey) < 32 {
		// The master key grants full cross-tenant admin. A short, low-entropy key
		// is brute-forceable despite the constant-time comparison. Warn loudly
		// rather than exit, to avoid breaking an already-bootstrapped deployment.
		slog.Warn("MASTER_API_KEY is shorter than 32 characters — it grants full admin access "+
			"and should be a high-entropy random secret; regenerate it (e.g. `openssl rand -hex 32`)",
			"length", len(cfg.Auth.MasterKey))
	}

	if problems := cfg.ProductionProblems(); len(problems) > 0 {
		if !cfg.Production.InsecureDev {
			for _, msg := range problems {
				slog.Error("insecure configuration", "problem", msg)
			}
			slog.Error("refusing to start with an insecure configuration; fix the problems above, " +
				"or set VAULTRUN_INSECURE_DEV=true for local development only " +
				"(see docs/production-hardening.md)")
			os.Exit(1)
		}
		slog.Warn("VAULTRUN_INSECURE_DEV=true — running with insecure settings; local development only "+
			"(see docs/production-hardening.md)", "problems", problems)
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
	// The API binary is static, so it can be the run supervisor in any image.
	if exe, err := os.Executable(); err == nil {
		docker.SetSupervisorBinary(exe)
	} else {
		slog.Warn("cannot locate the API binary; run timeouts will not stop commands", "err", err)
	}

	// WORKSPACE_QUOTA=xfs must work before the API accepts sessions: a
	// configured hard limit that silently does not apply is worse than none.
	switch cfg.Workspace.Quota {
	case "":
	case "xfs":
		if cfg.Workspace.MaxWorkspaceMB <= 0 {
			slog.Error("WORKSPACE_QUOTA=xfs needs MAX_WORKSPACE_MB > 0")
			os.Exit(1)
		}
		qctx, qcancel := context.WithTimeout(context.Background(), 5*time.Minute)
		err := newXFSQuota(cfg, db, docker).Check(qctx)
		qcancel()
		if err != nil {
			slog.Error("workspace quota unavailable — refusing to start", "err", err)
			os.Exit(1)
		}
		slog.Info("workspace hard quota enabled", "backend", "xfs", "max_mb", cfg.Workspace.MaxWorkspaceMB)
	default:
		slog.Error("WORKSPACE_QUOTA must be empty or xfs", "value", cfg.Workspace.Quota)
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
	guarded := docker.Guarded(context.Background())
	if guarded {
		slog.Info("docker access goes through the VaultRun docker guard: the API cannot start privileged containers or mount host paths")
	} else {
		slog.Warn("the API talks to the Docker daemon directly, which amounts to root on the host; " +
			"run it behind the docker guard (the docker-guard service in deployments/docker-compose.yml)")
	}

	// A DOCKER_RUNTIME the daemon does not know would fail every session (and
	// silently falling back to runc would drop the isolation the operator asked
	// for), so refuse to start instead.
	rtCtx, rtCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := docker.CheckRuntime(rtCtx); err != nil {
		slog.Error("DOCKER_RUNTIME is not usable — register it in the daemon's daemon.json or unset it",
			"runtime", docker.Runtime(), "err", err)
		rtCancel()
		os.Exit(1)
	}
	rtCancel()
	if err := docker.GPUSupported(); err != nil && cfg.Docker.GPUDevices != "" {
		slog.Warn("DOCKER_GPU_DEVICES is set but sessions with gpu_enabled will be rejected", "err", err)
	}

	// allowed_hosts needs host iptables. Inside a container the API's own
	// iptables would act on the wrong network namespace, so compose provides a
	// host-network helper container that the API drives with docker exec.
	if name := os.Getenv("NETPOLICY_CONTAINER"); name != "" {
		docker.UseNetpolicyContainer(name)
		slog.Info("network allowlist enforced via helper container", "container", name)
	}
	if !dockerpkg.EgressPolicyAvailable() {
		slog.Warn("allowed_hosts unavailable: no iptables on PATH and NETPOLICY_CONTAINER not set — " +
			"sessions that set allowed_hosts will be rejected")
	}

	ws := workspace.New(cfg.Workspace.BaseDir)
	if guarded {
		// The base directory belongs to root; the guard creates and removes
		// session directories.
		ws.SetDirOps(docker)
	}

	// Ensure base workspace dir exists (0o700 = owner-only; no group/world read)
	if err := os.MkdirAll(cfg.Workspace.BaseDir, 0o700); err != nil {
		slog.Error("create workspace base dir", "err", err)
		os.Exit(1)
	}
	// Verify the workspace directory is actually writable. A read-only mount or
	// wrong ownership would cause silent failures at session creation time.
	probeDir := cfg.Workspace.BaseDir
	if guarded {
		probeDir = filepath.Join(cfg.Workspace.BaseDir, "artifacts")
	}
	probeFile := filepath.Join(probeDir, ".vaultrun-startup-probe")
	if err := os.WriteFile(probeFile, []byte("ok"), 0o600); err != nil {
		slog.Error("workspace base dir is not writable — check WORKSPACE_BASE_DIR permissions",
			"dir", cfg.Workspace.BaseDir, "err", err)
		os.Exit(1)
	}
	_ = os.Remove(probeFile)

	al := audit.New(db, cfg.Observability.AuditHMACKey)

	if key := cfg.Observability.EvidenceSigningKey; key != "" {
		signer, err := evidence.ParseSigningKey(key)
		if err != nil {
			slog.Error("EVIDENCE_SIGNING_KEY is not usable", "err", err)
			os.Exit(1)
		}
		slog.Info("evidence packs enabled", "key_id", signer.KeyID())
		if url := cfg.Observability.EvidenceTSAURL; url != "" {
			if _, err := evidence.LoadCertPool(cfg.Observability.EvidenceTSACAFile); err != nil {
				slog.Error("EVIDENCE_TSA_CA_FILE is not usable", "err", err)
				os.Exit(1)
			}
			slog.Info("evidence packs are timestamped", "tsa", url)
		}
	} else {
		slog.Warn("evidence export disabled: set EVIDENCE_SIGNING_KEY to sign session evidence packs")
	}

	// Cost tracker for resource usage and billing (uses same key as audit HMAC).
	costTracker := cost.New(db, []byte(cfg.Observability.AuditHMACKey))

	// Session templates manager — marketplace and template-based session creation.
	templatesManager := templates.New(db)
	missionsManager := missions.New(db)
	// Bootstrap built-in templates (idempotent).
	if err := templatesManager.Bootstrap(context.Background()); err != nil {
		slog.Error("bootstrap templates", "err", err)
		os.Exit(1)
	}

	// Multi-agent collaboration — WebSocket + Redis pub/sub.
	var collabManager *collab.Manager
	var collabHub *collab.Hub
	if cfg.Redis.Addr != "" {
		redisClient := redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		// Test Redis connection
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			slog.Warn("redis ping failed, collaboration will be disabled", "err", err)
		} else {
			collabManager = collab.New(db, redisClient)
			collabHub = collab.NewHub(collabManager)
			go collabHub.Run(context.Background())
			slog.Info("multi-agent collaboration enabled", "redis", cfg.Redis.Addr)
		}
	} else {
		slog.Warn("REDIS_ADDR not set — multi-agent collaboration is disabled")
	}

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
	} else if !cfg.Production.OPAAllowAll {
		// M-8: warn operators who start without an explicit policy — AllowAll
		// permits every command and every file path with no restrictions.
		slog.Warn("OPA_POLICY_FILE not set — running with AllowAll policy; " +
			"all commands and file paths are permitted inside sandbox containers")
	}

	rnr := runner.New(db, docker, al, policyHook)

	// Replay manager for session checkpoints (uses same key as audit HMAC).
	replayMgr := replay.New(db, ws, []byte(cfg.Observability.AuditHMACKey))

	// Configure automatic checkpoint creation after command execution.
	// Only fires for sessions with replay_enabled = true.
	rnr.SetCheckpointHook(func(ctx context.Context, sessionID, runID uuid.UUID, exitCode int, durationMs int64, stdout, stderr string) error {
		var replayEnabled bool
		err := db.GetContext(ctx, &replayEnabled,
			"SELECT COALESCE(replay_enabled, false) FROM sessions WHERE id = $1", sessionID)
		if err != nil || !replayEnabled {
			return nil // Replay disabled, skip checkpoint
		}

		var cmd string
		var args []string
		err = db.QueryRowContext(ctx,
			"SELECT command, args FROM runs WHERE id = $1", runID).Scan(&cmd, &args)
		if err != nil {
			return err
		}

		durationMsInt := int(durationMs)
		_, err = replayMgr.CreateCheckpoint(ctx, replay.CreateCheckpointOpts{
			SessionID:   sessionID,
			RunID:       &runID,
			Description: fmt.Sprintf("Auto checkpoint after: %s", cmd),
			Command:     cmd,
			Args:        args,
			ExitCode:    &exitCode,
			DurationMs:  &durationMsInt,
			Stdout:      stdout,
			Stderr:      stderr,
		})
		return err
	})

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
		if guarded {
			pool.SetDirOps(docker)
		}
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

	// Close sessions whose container vanished while the API was down.
	reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 30*time.Second)
	cleanup.ReconcileOrphans(reconcileCtx, db, docker, al, ws)
	reconcileCancel()

	// Start background cleanup of idle sessions.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	idleFor := time.Duration(cfg.Docker.IdleTimeoutMins) * time.Minute
	go cleanup.Start(cleanupCtx, db, docker, al, ws, 5*time.Minute, idleFor, cfg.Observability.AuditLogRetentionDays)
	go costTracker.StartAccrual(cleanupCtx, time.Minute)
	go al.WatchIntegrity(cleanupCtx, 5*time.Minute)
	if cfg.Workspace.MaxWorkspaceMB > 0 {
		// Sandboxes write to their workspace on the host's disk directly;
		// pause the ones that grow past MAX_WORKSPACE_MB.
		q := &quota.Enforcer{DB: db, Docker: docker, WS: ws, Audit: al, MaxBytes: cfg.Workspace.MaxWorkspaceMB << 20}
		go q.Run(cleanupCtx, 5*time.Second)
	} else {
		slog.Warn("MAX_WORKSPACE_MB is 0: a sandbox can fill the host's disk — set a cap per session")
	}

	// SIEM audit export — optional; only active when AUDIT_EXPORT_URL is set.
	if siemExp := siemexport.New(db); siemExp != nil {
		siemExp.Start(cleanupCtx)
		slog.Info("siem audit export started", "url", os.Getenv("AUDIT_EXPORT_URL"))
	}

	// Artifact storage backend: S3 when configured, local filesystem otherwise.
	var artStore artifacts.Store
	if cfg.Workspace.ArtifactS3Bucket != "" {
		s3store, err := artifacts.NewS3Store(context.Background(), artifacts.S3Config{
			Bucket:          cfg.Workspace.ArtifactS3Bucket,
			Region:          cfg.Workspace.ArtifactS3Region,
			Prefix:          cfg.Workspace.ArtifactS3Prefix,
			Endpoint:        cfg.Workspace.ArtifactS3Endpoint,
			AccessKeyID:     cfg.Workspace.ArtifactS3AccessKeyID,
			SecretAccessKey: cfg.Workspace.ArtifactS3SecretAccessKey,
			ForcePathStyle:  cfg.Workspace.ArtifactS3ForcePathStyle,
		})
		if err != nil {
			slog.Error("create s3 artifact store", "err", err)
			os.Exit(1)
		}
		artStore = s3store
		slog.Info("artifact storage: s3",
			"bucket", cfg.Workspace.ArtifactS3Bucket,
			"region", cfg.Workspace.ArtifactS3Region)
	} else {
		artDir := cfg.Workspace.BaseDir + "/artifacts"
		artStore = artifacts.NewLocalStore(artDir)
		slog.Info("artifact storage: local filesystem", "dir", artDir)
	}

	// ── Enterprise features (ee/): SSO is wired in via build tag.
	ent := initEnterprise(cfg, db, al)

	r := newRouter(cfg, db, docker, ws, rnr, al, policyHook, queue, sec, pool, artStore, costTracker, templatesManager,
		missionsManager, collabManager, collabHub, replayMgr, ent)

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
				Email:      cfg.Server.ACMEEmail,
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
