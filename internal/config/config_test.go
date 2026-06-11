package config

import (
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
