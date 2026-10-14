package db

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/nickvd7/vaultrun/internal/config"
)

func Connect(cfg config.DatabaseConfig) (*sqlx.DB, error) {
	if err := config.ValidateDBSSLMode(cfg.SSLMode); err != nil {
		return nil, err
	}

	dsn := withConnectTimeout(injectSSLParams(cfg.DSN, cfg), connectTimeoutSeconds)

	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	// Warn operators when the database connection is unencrypted.
	// In production, use DB_SSL_MODE=require or verify-full.
	if strings.Contains(dsn, "sslmode=disable") || cfg.SSLMode == "disable" {
		slog.Warn("database connection is unencrypted (sslmode=disable) — " +
			"set DB_SSL_MODE=require in production")
	}

	slog.Info("connecting to database", "timeout_seconds", connectTimeoutSeconds)
	ctx, cancel := context.WithTimeout(context.Background(), (connectTimeoutSeconds+5)*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping db (is Postgres reachable?): %w", err)
	}

	return db, nil
}

// connectTimeoutSeconds bounds how long a single connection attempt may block.
// database/sql hands the context to lib/pq's Connect but lib/pq (v1.10) dials
// without it, so without connect_timeout an unreachable Postgres blocks for the
// OS TCP timeout (minutes) without a single log line.
const connectTimeoutSeconds = 10

// withConnectTimeout adds connect_timeout to the DSN unless the caller already set one.
func withConnectTimeout(dsn string, seconds int) string {
	if strings.Contains(dsn, "connect_timeout") {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		q.Set("connect_timeout", strconv.Itoa(seconds))
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + " connect_timeout=" + strconv.Itoa(seconds)
}

func RunMigrations(db *sqlx.DB, migrationsPath string) error {
	driver, err := postgres.WithInstance(db.DB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://"+migrationsPath,
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

// injectSSLParams appends or overrides SSL/TLS query parameters in a Postgres
// DSN based on the provided DatabaseConfig. Parameters supplied via env vars
// (SSLMode, SSLRootCert, SSLCert, SSLKey) take precedence over whatever is
// already in the DSN string.
//
// If none of the SSL fields are set, the DSN is returned unchanged.
// A warning is emitted when sslmode=disable is detected, to alert operators
// running in environments where encryption should be enabled.
func injectSSLParams(dsn string, cfg config.DatabaseConfig) string {
	// Nothing to inject — return DSN as-is.
	if cfg.SSLMode == "" && cfg.SSLRootCert == "" && cfg.SSLCert == "" && cfg.SSLKey == "" {
		// Still warn if the DSN itself contains sslmode=disable, so developers
		// are aware when running without TLS.
		if strings.Contains(dsn, "sslmode=disable") {
			slog.Warn("db: sslmode=disable detected in DATABASE_URL — TLS is OFF; " +
				"set DB_SSL_MODE to enable encryption")
		}
		return dsn
	}

	u, err := url.Parse(dsn)
	if err != nil {
		// Cannot parse the DSN as a URL — fall back to raw string manipulation.
		slog.Warn("db: cannot parse DATABASE_URL for SSL injection, using DSN as-is", "err", err)
		return dsn
	}

	q := u.Query()

	if cfg.SSLMode != "" {
		q.Set("sslmode", cfg.SSLMode)
	}
	if cfg.SSLRootCert != "" {
		q.Set("sslrootcert", cfg.SSLRootCert)
	}
	if cfg.SSLCert != "" {
		q.Set("sslcert", cfg.SSLCert)
	}
	if cfg.SSLKey != "" {
		q.Set("sslkey", cfg.SSLKey)
	}

	u.RawQuery = q.Encode()
	result := u.String()

	// Final guard: warn if the resulting DSN still has sslmode=disable,
	// which can happen when the DSN had it and DB_SSL_MODE was not set.
	if q.Get("sslmode") == "disable" {
		slog.Warn("db: effective sslmode=disable — TLS is OFF; " +
			"set DB_SSL_MODE=require (or higher) to enforce encryption")
	}

	return result
}
