package config

import (
	"fmt"
	"os"
	"strconv"
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
	Host              string
	TLSVerify         bool
	CertPath          string
	NetworkName       string
	DefaultImage      string
	ContainerPrefix   string
	IdleTimeoutMins   int
}

type WorkspaceConfig struct {
	BaseDir     string
	MaxFileMB   int64
	MaxOutputMB int64
}

type AuthConfig struct {
	MasterKey string
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

	cfg := &Config{
		Server: ServerConfig{
			Host:            getEnv("HOST", "0.0.0.0"),
			Port:            port,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    120 * time.Second,
			ShutdownTimeout: 15 * time.Second,
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
		},
		Workspace: WorkspaceConfig{
			BaseDir:     getEnv("WORKSPACE_BASE_DIR", "/data/workspaces"),
			MaxFileMB:   maxFileMB,
			MaxOutputMB: maxOutputMB,
		},
		Auth: AuthConfig{
			MasterKey: getEnv("MASTER_API_KEY", ""),
		},
	}

	return cfg, nil
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
