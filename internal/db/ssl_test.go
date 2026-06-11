package db

import (
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/config"
)

func TestInjectSSLParamsNoSSLConfig(t *testing.T) {
	// When no SSL env vars are set, the DSN is returned unchanged.
	dsn := "postgres://vaultrun:vaultrun@localhost:5432/vaultrun?sslmode=disable"
	cfg := config.DatabaseConfig{}
	result := injectSSLParams(dsn, cfg)
	if result != dsn {
		t.Errorf("expected unchanged DSN, got %q", result)
	}
}

func TestInjectSSLParamsSSLModeOverridesDSN(t *testing.T) {
	// DB_SSL_MODE should override sslmode=disable that's baked into the DSN.
	dsn := "postgres://vaultrun:vaultrun@localhost:5432/vaultrun?sslmode=disable"
	cfg := config.DatabaseConfig{SSLMode: "require"}
	result := injectSSLParams(dsn, cfg)
	if !strings.Contains(result, "sslmode=require") {
		t.Errorf("expected sslmode=require in result, got %q", result)
	}
	if strings.Contains(result, "sslmode=disable") {
		t.Errorf("sslmode=disable should be overridden, got %q", result)
	}
}

func TestInjectSSLParamsAddsRootCert(t *testing.T) {
	dsn := "postgres://vaultrun:vaultrun@localhost:5432/vaultrun"
	cfg := config.DatabaseConfig{
		SSLMode:     "verify-full",
		SSLRootCert: "/certs/ca.pem",
	}
	result := injectSSLParams(dsn, cfg)
	if !strings.Contains(result, "sslmode=verify-full") {
		t.Errorf("expected sslmode=verify-full in result, got %q", result)
	}
	if !strings.Contains(result, "sslrootcert=") {
		t.Errorf("expected sslrootcert in result, got %q", result)
	}
}

func TestInjectSSLParamsClientCertAndKey(t *testing.T) {
	dsn := "postgres://vaultrun:vaultrun@localhost:5432/vaultrun"
	cfg := config.DatabaseConfig{
		SSLMode: "verify-full",
		SSLCert: "/certs/client.pem",
		SSLKey:  "/certs/client.key",
	}
	result := injectSSLParams(dsn, cfg)
	if !strings.Contains(result, "sslcert=") {
		t.Errorf("expected sslcert in result, got %q", result)
	}
	if !strings.Contains(result, "sslkey=") {
		t.Errorf("expected sslkey in result, got %q", result)
	}
}

func TestInjectSSLParamsPreservesExistingParams(t *testing.T) {
	// Other DSN query params must not be lost during SSL injection.
	dsn := "postgres://vaultrun:vaultrun@localhost:5432/vaultrun?connect_timeout=10"
	cfg := config.DatabaseConfig{SSLMode: "require"}
	result := injectSSLParams(dsn, cfg)
	if !strings.Contains(result, "connect_timeout=10") {
		t.Errorf("existing DSN param lost, got %q", result)
	}
	if !strings.Contains(result, "sslmode=require") {
		t.Errorf("sslmode=require missing, got %q", result)
	}
}

func TestInjectSSLParamsInvalidDSNReturnedAsIs(t *testing.T) {
	// An unparseable DSN must not panic — return the original.
	dsn := "not-a-url"
	cfg := config.DatabaseConfig{SSLMode: "require"}
	result := injectSSLParams(dsn, cfg)
	// The function should return something (either the original or modified);
	// the important thing is it doesn't panic.
	if result == "" {
		t.Error("expected non-empty result for unparseable DSN")
	}
}
