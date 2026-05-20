package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Redis     RedisConfig
	Docker    DockerConfig
	Workspace WorkspaceConfig
	Auth      AuthConfig
}

type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	CORSOrigins     []string // allowed CORS origins; empty = same-origin only
	RateLimit       int      // max requests per minute per IP (0 = disabled)
}

type DatabaseConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type DockerConfig struct {
	Host            string
	TLSVerify       bool
	CertPath        string
	NetworkName     string
	DefaultImage    string
	ContainerPrefix string
	IdleTimeoutMins int
	// ImageAllowlist is an optional set of permitted images.
	// An empty slice means all images are allowed.
	ImageAllowlist []string
}

type WorkspaceConfig struct {
	BaseDir     string
	MaxFileMB   int64
	MaxOutputMB int64
}

type AuthConfig struct {
	MasterKey     string
	OPAPolicyFile string // optional path to a Rego policy file; empty = AllowAll
}

// Limits caps applied to session creation requests.
type SessionLimits struct {
	MaxCPU        float64
	MaxMemoryMB   int
	MaxTimeoutSec int
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
	maxOutputMB, _ := strconv.ParseInt(getEnv("MAX_OUTPUT_MB", "10"), 10, 64)
	rateLimit, _ := strconv.Atoi(getEnv("RATE_LIMIT_PER_MIN", "120"))

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
		},
		Database: DatabaseConfig{
			DSN:             getEnv("DATABASE_URL", "postgres://vaultrun:vaultrun@localhost:5432/vaultrun?sslmode=disable"),
			MaxOpenConns:    dbMaxOpen,
			MaxIdleConns:    dbMaxIdle,
			ConnMaxLifetime: 5 * time.Minute,
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
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
		},
		Workspace: WorkspaceConfig{
			BaseDir:     getEnv("WORKSPACE_BASE_DIR", "/data/workspaces"),
			MaxFileMB:   maxFileMB,
			MaxOutputMB: maxOutputMB,
		},
		Auth: AuthConfig{
			MasterKey:     getEnv("MASTER_API_KEY", ""),
			OPAPolicyFile: getEnv("OPA_POLICY_FILE", ""),
		},
	}

	return cfg, nil
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
	return SessionLimits{MaxCPU: maxCPU, MaxMemoryMB: maxMem, MaxTimeoutSec: maxTO}
}

// ImageAllowed returns true if the given image is permitted.
// When the allowlist is empty, any image is allowed.
func (c *Config) ImageAllowed(image string) bool {
	if len(c.Docker.ImageAllowlist) == 0 {
		return true
	}
	for _, allowed := range c.Docker.ImageAllowlist {
		if allowed == image {
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
