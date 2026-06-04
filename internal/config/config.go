package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Redis         RedisConfig
	Docker        DockerConfig
	Workspace     WorkspaceConfig
	Auth          AuthConfig
	Observability ObservabilityConfig
}

type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	CORSOrigins     []string // allowed CORS origins; empty = same-origin only
	RateLimit       int      // max requests per minute per IP (0 = disabled)
	ActorRateLimit  int      // max requests per minute per actor/API-key (0 = same as RateLimit; -1 = disabled)
	// Static TLS: when both are set the server listens over HTTPS using the provided cert/key files.
	TLSCertFile string // TLS_CERT_FILE — PEM certificate chain
	TLSKeyFile  string // TLS_KEY_FILE  — PEM private key
	// ACME / Let's Encrypt: when set, the server obtains and auto-renews a certificate.
	// Requires port 443 access and a publicly routable hostname.
	// Takes precedence over TLS_CERT_FILE/TLS_KEY_FILE when set.
	ACMEDomain   string // ACME_DOMAIN  — hostname to obtain a cert for (e.g. "api.example.com")
	ACMECacheDir string // ACME_CACHE_DIR — directory to persist ACME account keys + certs (default: /data/acme-cache)
}

type DatabaseConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// TLS / SSL configuration for the PostgreSQL connection.
	// These take effect via injectSSLParams in internal/db/db.go and override
	// any sslmode already present in the DSN.
	SSLMode     string // DB_SSL_MODE: disable|allow|prefer|require|verify-ca|verify-full
	SSLRootCert string // DB_SSL_ROOT_CERT: path to CA certificate file
	SSLCert     string // DB_SSL_CERT: path to client certificate file
	SSLKey      string // DB_SSL_KEY: path to client private key file
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type DockerConfig struct {
	Host            string
	NetworkName     string
	DefaultImage    string
	ContainerPrefix string
	IdleTimeoutMins int
	// ImageAllowlist is an optional set of permitted images.
	// An empty slice means all images are allowed.
	ImageAllowlist []string
	// WarmPoolSize is the number of containers to pre-start for WarmPoolImage.
	// 0 = disabled (default).
	WarmPoolSize int
	// WarmPoolImage is the image to pre-warm (e.g. "python:3.12-slim").
	// When empty, warm pool is disabled regardless of WarmPoolSize.
	WarmPoolImage string
	// GPUDevices enables NVIDIA GPU access in sessions that request it.
	// "all" = all GPUs; "0,1" = specific GPU indices; "" = disabled.
	GPUDevices string
}

type WorkspaceConfig struct {
	BaseDir              string
	MaxFileMB            int64
	MaxOutputMB          int64
	MaxWorkspaceMB       int64 // MAX_WORKSPACE_MB: total workspace size cap per session (0 = unlimited)
	MaxArtifactStorageMB int64 // MAX_ARTIFACT_STORAGE_MB: total artifact storage cap per actor (0 = unlimited)
	// S3 artifact storage — when ArtifactS3Bucket is set, artifacts are stored
	// in S3 instead of the local filesystem. Supports AWS S3 and S3-compatible
	// stores (MinIO, etc.) via the endpoint override.
	ArtifactS3Bucket          string // ARTIFACT_S3_BUCKET
	ArtifactS3Region          string // ARTIFACT_S3_REGION (default: us-east-1)
	ArtifactS3Prefix          string // ARTIFACT_S3_PREFIX (optional key prefix, e.g. "artifacts/")
	ArtifactS3Endpoint        string // ARTIFACT_S3_ENDPOINT (custom endpoint for MinIO / S3-compatible)
	ArtifactS3AccessKeyID     string // ARTIFACT_S3_ACCESS_KEY_ID
	ArtifactS3SecretAccessKey string // ARTIFACT_S3_SECRET_ACCESS_KEY
	ArtifactS3ForcePathStyle  bool   // ARTIFACT_S3_FORCE_PATH_STYLE (required for MinIO)
}

type AuthConfig struct {
	MasterKey     string
	OPAPolicyFile string // optional path to a Rego policy file; empty = AllowAll
}

// ObservabilityConfig groups logging and metrics knobs.
type ObservabilityConfig struct {
	LogLevel                 string // LOG_LEVEL: debug|info|warn|error (default: info)
	StopContainersOnShutdown bool   // STOP_CONTAINERS_ON_SHUTDOWN: gracefully stop all running containers on SIGTERM
	WebhookSecret            string // WEBHOOK_SECRET: HMAC-SHA256 key for signing async-run callback payloads
	AuditLogRetentionDays    int    // AUDIT_LOG_RETENTION_DAYS: delete audit logs older than N days (0 = keep forever)
	AuditHMACKey             string // AUDIT_HMAC_KEY: HMAC-SHA256 key for audit log integrity signatures; empty = disabled
}

// Limits caps applied to session creation requests.
type SessionLimits struct {
	MaxCPU              float64
	MaxMemoryMB         int
	MaxTimeoutSec       int
	MaxSessionsPerActor int // 0 = unlimited
}

func Load() (*Config, error) {
	port, err := strconv.Atoi(getEnv("PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("invalid PORT: %w", err)
	}

	dbMaxOpen, _ := strconv.Atoi(getEnv("DB_MAX_OPEN_CONNS", "25"))
	dbMaxIdle, _ := strconv.Atoi(getEnv("DB_MAX_IDLE_CONNS", "5"))
	redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))
	idleTimeout, _ := strconv.Atoi(getEnv("DOCKER_IDLE_TIMEOUT_MINS", "30"))
	maxFileMB, _ := strconv.ParseInt(getEnv("MAX_FILE_MB", "100"), 10, 64)
	// Clamp to a sane range. Without this, a huge value overflows the int64
	// byte computation (MB*1024*1024) to a negative number, which makes
	// io.LimitReader return EOF immediately and silently writes 0-byte files.
	maxFileMB = clampInt64(maxFileMB, 1, 1<<20) // 1 MB .. 1 TB
	maxOutputMB, _ := strconv.ParseInt(getEnv("MAX_OUTPUT_MB", "10"), 10, 64)
	maxOutputMB = clampInt64(maxOutputMB, 1, 1<<20)
	rateLimit, _ := strconv.Atoi(getEnv("RATE_LIMIT_PER_MIN", "120"))
	actorRateLimit, _ := strconv.Atoi(getEnv("ACTOR_RATE_LIMIT_PER_MIN", "0")) // 0 = inherit RateLimit
	stopOnShutdown, _ := strconv.ParseBool(getEnv("STOP_CONTAINERS_ON_SHUTDOWN", "false"))

	// CORS origins: comma-separated list; empty string means deny all cross-origin requests
	corsOrigins := splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", ""))

	// Optional image allowlist: comma-separated; empty = allow all
	imageAllowlist := splitAndTrim(getEnv("DOCKER_IMAGE_ALLOWLIST", ""))

	cfg := &Config{
		Server: ServerConfig{
			Host:            getEnv("HOST", "0.0.0.0"),
			Port:            port,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    120 * time.Second,
			ShutdownTimeout: 15 * time.Second,
			CORSOrigins:     corsOrigins,
			RateLimit:       rateLimit,
			ActorRateLimit:  actorRateLimit,
			TLSCertFile:     getEnv("TLS_CERT_FILE", ""),
			TLSKeyFile:      getEnv("TLS_KEY_FILE", ""),
			ACMEDomain:      getEnv("ACME_DOMAIN", ""),
			ACMECacheDir:    getEnv("ACME_CACHE_DIR", "/data/acme-cache"),
		},
		Database: DatabaseConfig{
			DSN:             getEnv("DATABASE_URL", "postgres://vaultrun:vaultrun@localhost:5432/vaultrun?sslmode=prefer"),
			MaxOpenConns:    dbMaxOpen,
			MaxIdleConns:    dbMaxIdle,
			ConnMaxLifetime: 5 * time.Minute,
			// SSL/TLS: these env vars override whatever is in the DSN.
			// DB_SSL_MODE defaults to "prefer" so that a stray sslmode=disable in a
			// custom DATABASE_URL is upgraded to an encrypted connection where the
			// server supports it (prefer still falls back to plaintext if it does
			// not, so connectivity is preserved). Operators must explicitly set
			// DB_SSL_MODE=disable to opt out.
			SSLMode:     getEnv("DB_SSL_MODE", "prefer"),
			SSLRootCert: getEnv("DB_SSL_ROOT_CERT", ""),
			SSLCert:     getEnv("DB_SSL_CERT", ""),
			SSLKey:      getEnv("DB_SSL_KEY", ""),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", ""),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       redisDB,
		},
		Docker: DockerConfig{
			Host:            getEnv("DOCKER_HOST", "unix:///var/run/docker.sock"),
			NetworkName:     getEnv("DOCKER_NETWORK", "none"),
			DefaultImage:    getEnv("DOCKER_DEFAULT_IMAGE", "python:3.12-slim"),
			ContainerPrefix: getEnv("DOCKER_CONTAINER_PREFIX", "vaultrun"),
			IdleTimeoutMins: idleTimeout,
			ImageAllowlist:  imageAllowlist,
			WarmPoolSize:    func() int { n, _ := strconv.Atoi(getEnv("WARM_POOL_SIZE", "0")); return n }(),
			WarmPoolImage:   getEnv("WARM_POOL_IMAGE", ""),
			GPUDevices:      getEnv("DOCKER_GPU_DEVICES", ""),
		},
		Workspace: WorkspaceConfig{
			BaseDir:              getEnv("WORKSPACE_BASE_DIR", "/data/workspaces"),
			MaxFileMB:            maxFileMB,
			MaxOutputMB:          maxOutputMB,
			MaxWorkspaceMB:       func() int64 { n, _ := strconv.ParseInt(getEnv("MAX_WORKSPACE_MB", "0"), 10, 64); return n }(),
			MaxArtifactStorageMB: func() int64 { n, _ := strconv.ParseInt(getEnv("MAX_ARTIFACT_STORAGE_MB", "0"), 10, 64); return n }(),
			ArtifactS3Bucket:          getEnv("ARTIFACT_S3_BUCKET", ""),
			ArtifactS3Region:          getEnv("ARTIFACT_S3_REGION", "us-east-1"),
			ArtifactS3Prefix:          getEnv("ARTIFACT_S3_PREFIX", ""),
			ArtifactS3Endpoint:        getEnv("ARTIFACT_S3_ENDPOINT", ""),
			ArtifactS3AccessKeyID:     getEnv("ARTIFACT_S3_ACCESS_KEY_ID", ""),
			ArtifactS3SecretAccessKey: getEnv("ARTIFACT_S3_SECRET_ACCESS_KEY", ""),
			ArtifactS3ForcePathStyle:  func() bool { v, _ := strconv.ParseBool(getEnv("ARTIFACT_S3_FORCE_PATH_STYLE", "false")); return v }(),
		},
		Auth: AuthConfig{
			MasterKey:     getEnv("MASTER_API_KEY", ""),
			OPAPolicyFile: getEnv("OPA_POLICY_FILE", ""),
		},
		Observability: ObservabilityConfig{
			LogLevel:                 getEnv("LOG_LEVEL", "info"),
			StopContainersOnShutdown: stopOnShutdown,
			WebhookSecret:            getEnv("WEBHOOK_SECRET", ""),
			AuditLogRetentionDays:    func() int { n, _ := strconv.Atoi(getEnv("AUDIT_LOG_RETENTION_DAYS", "90")); return n }(),
			AuditHMACKey:             getEnv("AUDIT_HMAC_KEY", ""),
		},
	}

	return cfg, nil
}

// TLSEnabled returns true when static TLS cert+key files are configured.
func (c *Config) TLSEnabled() bool {
	return c.Server.TLSCertFile != "" && c.Server.TLSKeyFile != ""
}

// ACMEEnabled returns true when ACME/Let's Encrypt automatic cert management is configured.
// ACME takes precedence over static TLS files when both are set.
func (c *Config) ACMEEnabled() bool {
	return c.Server.ACMEDomain != ""
}

// ActorRateLimitPerMin returns the effective per-actor rate limit.
// When ActorRateLimit is 0 it inherits RateLimit; -1 disables it.
func (c *Config) ActorRateLimitPerMin() int {
	if c.Server.ActorRateLimit == 0 {
		return c.Server.RateLimit
	}
	return c.Server.ActorRateLimit
}

// SessionLimits returns the hard caps for session resource requests.
func (c *Config) SessionLimits() SessionLimits {
	maxCPU, _ := strconv.ParseFloat(getEnv("MAX_SESSION_CPU", "8"), 64)
	maxMem, _ := strconv.Atoi(getEnv("MAX_SESSION_MEMORY_MB", "8192"))
	maxTO, _ := strconv.Atoi(getEnv("MAX_SESSION_TIMEOUT_SEC", "86400"))
	if maxCPU <= 0 {
		maxCPU = 8
	}
	if maxMem <= 0 {
		maxMem = 8192
	}
	if maxTO <= 0 {
		maxTO = 86400
	}
	maxSessions, _ := strconv.Atoi(getEnv("MAX_SESSIONS_PER_ACTOR", "20"))
	if maxSessions < 0 {
		maxSessions = 0
	}
	return SessionLimits{MaxCPU: maxCPU, MaxMemoryMB: maxMem, MaxTimeoutSec: maxTO, MaxSessionsPerActor: maxSessions}
}

// ImageAllowed returns true if the given image is permitted.
// When the allowlist is empty, any image is allowed.
func (c *Config) ImageAllowed(image string) bool {
	if len(c.Docker.ImageAllowlist) == 0 {
		return true
	}
	// Case-insensitive comparison: Docker normalises image names to lowercase
	// internally, so "Python:3.12-slim" and "python:3.12-slim" refer to the
	// same image. A case-sensitive allowlist would be trivially bypassed.
	for _, allowed := range c.Docker.ImageAllowlist {
		if strings.EqualFold(allowed, image) {
			return true
		}
	}
	return false
}

func (c *Config) ServerAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// clampInt64 bounds v to [min, max]. Used to keep operator-supplied size limits
// within a range that cannot overflow when later multiplied into a byte count.
func clampInt64(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
