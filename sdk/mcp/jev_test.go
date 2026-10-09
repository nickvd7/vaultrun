package main

import (
	"os"
	"testing"
)

func TestJevDisabledByDefault(t *testing.T) {
	os.Unsetenv("MCP_JEV_ENABLED")
	if jevEnabled() {
		t.Fatal("jev should be off by default")
	}
	t.Setenv("MCP_JEV_ENABLED", "true")
	if !jevEnabled() {
		t.Fatal("expected enabled")
	}
}

func TestJevToolDefinitionsCount(t *testing.T) {
	if len(jevToolDefinitions()) != 2 {
		t.Fatalf("want 2 tools, got %d", len(jevToolDefinitions()))
	}
}

func TestToolDefinitionsJevOptIn(t *testing.T) {
	t.Setenv("MCP_JEV_ENABLED", "")
	base := len(toolDefinitions())
	t.Setenv("MCP_JEV_ENABLED", "true")
	with := len(toolDefinitions())
	if with != base+2 {
		t.Fatalf("want %d tools with jev, got %d (base %d)", base+2, with, base)
	}
}

func TestParseJevClaims(t *testing.T) {
	arr, err := parseJevClaims(map[string]string{"claims": `["a","b"]`})
	if err != nil || len(arr) != 2 {
		t.Fatalf("%v %v", arr, err)
	}
	arr, err = parseJevClaims(map[string]string{"claim": "only"})
	if err != nil || len(arr) != 1 || arr[0] != "only" {
		t.Fatalf("%v %v", arr, err)
	}
	_, err = parseJevClaims(map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestJevOrErrDisabled(t *testing.T) {
	t.Setenv("MCP_JEV_ENABLED", "")
	s := &server{}
	_, err := s.jevOrErr()
	if err == nil {
		t.Fatal("expected disabled error")
	}
}
