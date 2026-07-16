package main

import (
	"os"
	"testing"
)

func TestFlowdToolDefinitionsCount(t *testing.T) {
	if len(flowdToolDefinitions()) != 6 {
		t.Fatalf("want 6 flowd tools, got %d", len(flowdToolDefinitions()))
	}
}

func TestToolDefinitionsFlowdOptIn(t *testing.T) {
	t.Setenv("MCP_FLOWD_ENABLED", "")
	base := len(toolDefinitions())

	t.Setenv("MCP_FLOWD_ENABLED", "true")
	withFlowd := len(toolDefinitions())
	if withFlowd != base+6 {
		t.Fatalf("with Flowd: want %d tools, got %d", base+6, withFlowd)
	}
}

func TestFlowdDisabledByDefault(t *testing.T) {
	os.Unsetenv("MCP_FLOWD_ENABLED")
	if flowdEnabled() {
		t.Fatal("flowd should be disabled by default")
	}
	t.Setenv("MCP_FLOWD_ENABLED", "true")
	if !flowdEnabled() {
		t.Fatal("flowd should be enabled when MCP_FLOWD_ENABLED=true")
	}
}
