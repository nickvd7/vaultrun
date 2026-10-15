package config

import (
	"strings"
	"testing"
)

func TestImageAllowedEmptyList(t *testing.T) {
	c := &Config{Docker: DockerConfig{ImageAllowlist: nil}}
	if !c.ImageAllowed("anything:latest") {
		t.Fatal("empty allowlist should permit any image")
	}
}

func TestImageAllowedMatchFound(t *testing.T) {
	c := &Config{Docker: DockerConfig{ImageAllowlist: []string{"python:3.12-slim", "node:20-slim"}}}
	if !c.ImageAllowed("python:3.12-slim") {
		t.Fatal("exact match should be allowed")
	}
}

func TestImageAllowedNoMatch(t *testing.T) {
	c := &Config{Docker: DockerConfig{ImageAllowlist: []string{"python:3.12-slim"}}}
	if c.ImageAllowed("ubuntu:latest") {
		t.Fatal("non-listed image should be denied")
	}
}

func TestImageAllowedPartialMatchDenied(t *testing.T) {
	c := &Config{Docker: DockerConfig{ImageAllowlist: []string{"python:3.12-slim"}}}
	if c.ImageAllowed("python:3.12") {
		t.Fatal("partial tag match must not be allowed")
	}
}

func TestSessionLimitsDefaults(t *testing.T) {
	// Unset env — defaults apply
	t.Setenv("MAX_SESSION_CPU", "")
	t.Setenv("MAX_SESSION_MEMORY_MB", "")
	t.Setenv("MAX_SESSION_TIMEOUT_SEC", "")

	c := &Config{}
	lim := c.SessionLimits()

	if lim.MaxCPU != 8 {
		t.Fatalf("default MaxCPU should be 8, got %f", lim.MaxCPU)
	}
	if lim.MaxMemoryMB != 8192 {
		t.Fatalf("default MaxMemoryMB should be 8192, got %d", lim.MaxMemoryMB)
	}
	if lim.MaxTimeoutSec != 86400 {
		t.Fatalf("default MaxTimeoutSec should be 86400, got %d", lim.MaxTimeoutSec)
	}
}

func TestSessionLimitsFromEnv(t *testing.T) {
	t.Setenv("MAX_SESSION_CPU", "4")
	t.Setenv("MAX_SESSION_MEMORY_MB", "2048")
	t.Setenv("MAX_SESSION_TIMEOUT_SEC", "3600")

	c := &Config{}
	lim := c.SessionLimits()

	if lim.MaxCPU != 4 {
		t.Fatalf("expected MaxCPU=4, got %f", lim.MaxCPU)
	}
	if lim.MaxMemoryMB != 2048 {
		t.Fatalf("expected MaxMemoryMB=2048, got %d", lim.MaxMemoryMB)
	}
	if lim.MaxTimeoutSec != 3600 {
		t.Fatalf("expected MaxTimeoutSec=3600, got %d", lim.MaxTimeoutSec)
	}
}

func TestSessionLimitsZeroFallback(t *testing.T) {
	t.Setenv("MAX_SESSION_CPU", "0")
	t.Setenv("MAX_SESSION_MEMORY_MB", "0")
	t.Setenv("MAX_SESSION_TIMEOUT_SEC", "0")

	c := &Config{}
	lim := c.SessionLimits()

	if lim.MaxCPU <= 0 {
		t.Fatal("zero CPU should fall back to default (> 0)")
	}
	if lim.MaxMemoryMB <= 0 {
		t.Fatal("zero memory should fall back to default (> 0)")
	}
	if lim.MaxTimeoutSec <= 0 {
		t.Fatal("zero timeout should fall back to default (> 0)")
	}
}

func TestSplitAndTrimEmpty(t *testing.T) {
	if r := splitAndTrim(""); r != nil {
		t.Fatalf("empty string should return nil, got %v", r)
	}
}

func TestSplitAndTrimSingle(t *testing.T) {
	r := splitAndTrim("python:3.12-slim")
	if len(r) != 1 || r[0] != "python:3.12-slim" {
		t.Fatalf("unexpected result: %v", r)
	}
}

func TestSplitAndTrimMultipleWithSpaces(t *testing.T) {
	r := splitAndTrim("  python:3.12-slim , node:20-slim , ")
	if len(r) != 2 {
		t.Fatalf("expected 2 entries, got %v", r)
	}
	if r[0] != "python:3.12-slim" || r[1] != "node:20-slim" {
		t.Fatalf("unexpected values: %v", r)
	}
}

func TestServerAddrFormat(t *testing.T) {
	c := &Config{Server: ServerConfig{Host: "0.0.0.0", Port: 8080}}
	if addr := c.ServerAddr(); addr != "0.0.0.0:8080" {
		t.Fatalf("unexpected addr: %s", addr)
	}
}

func TestValidateDBSSLMode(t *testing.T) {
	ok := []string{"", "disable", "require", "verify-ca", "verify-full"}
	for _, mode := range ok {
		if err := ValidateDBSSLMode(mode); err != nil {
			t.Errorf("ValidateDBSSLMode(%q) unexpected error: %v", mode, err)
		}
	}
	for _, mode := range []string{"prefer", "allow", "bogus"} {
		if err := ValidateDBSSLMode(mode); err == nil {
			t.Errorf("ValidateDBSSLMode(%q) expected error", mode)
		}
	}
}

func TestLoadDefaultDatabaseSSLLeavesDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_SSL_MODE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.SSLMode != "" {
		t.Fatalf("default SSLMode should be empty so DSN sslmode is used, got %q", cfg.Database.SSLMode)
	}
	if !strings.Contains(cfg.Database.DSN, "sslmode=disable") {
		t.Fatalf("default DSN should use sslmode=disable, got %q", cfg.Database.DSN)
	}
}

func TestLoadRejectsPreferSSLMode(t *testing.T) {
	t.Setenv("DB_SSL_MODE", "prefer")
	if _, err := Load(); err == nil {
		t.Fatal("Load should reject DB_SSL_MODE=prefer")
	}
}

func hardenedConfig() *Config {
	return &Config{
		Auth:          AuthConfig{MasterKey: strings.Repeat("k", 32), OPAPolicyFile: "policy.rego"},
		Server:        ServerConfig{TLSCertFile: "c.pem", TLSKeyFile: "k.pem"},
		Production:    ProductionConfig{MetricsToken: "t"},
		Observability: ObservabilityConfig{AuditHMACKey: "audit-key"},
	}
}

func TestProductionProblemsHardenedConfigIsClean(t *testing.T) {
	if p := hardenedConfig().ProductionProblems(); len(p) != 0 {
		t.Fatalf("unexpected problems: %v", p)
	}
}

func TestProductionProblemsQuickstartDefaults(t *testing.T) {
	c := &Config{Auth: AuthConfig{MasterKey: "short"}}
	got := strings.Join(c.ProductionProblems(), "\n")
	for _, want := range []string{"OPA", "TLS", "METRICS_TOKEN", "MASTER_API_KEY"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in problems:\n%s", want, got)
		}
	}
}

func TestProductionProblemsExplicitOptOuts(t *testing.T) {
	c := hardenedConfig()
	c.Auth.OPAPolicyFile = ""
	c.Server.TLSCertFile, c.Server.TLSKeyFile = "", ""
	if len(c.ProductionProblems()) != 2 {
		t.Fatalf("expected OPA and TLS problems, got %v", c.ProductionProblems())
	}
	c.Production.OPAAllowAll = true
	c.Production.TLSDisabled = true
	if p := c.ProductionProblems(); len(p) != 0 {
		t.Fatalf("explicit opt-outs should satisfy the check: %v", p)
	}
}

func TestProductionProblemsRedisPasswordOnlyWhenRedisUsed(t *testing.T) {
	c := hardenedConfig()
	if len(c.ProductionProblems()) != 0 {
		t.Fatal("no redis configured: no problem expected")
	}
	c.Redis.Addr = "redis:6379"
	if len(c.ProductionProblems()) != 1 {
		t.Fatalf("redis without password should be flagged: %v", c.ProductionProblems())
	}
}

func TestProductionProblemsEmptyMasterKeyAllowed(t *testing.T) {
	c := hardenedConfig()
	c.Auth.MasterKey = ""
	if p := c.ProductionProblems(); len(p) != 0 {
		t.Fatalf("empty key (cleared after bootstrap) must not be flagged: %v", p)
	}
}
