package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCoerceToolArgs_TypedJSON(t *testing.T) {
	raw := json.RawMessage(`{
		"session_id":"s1",
		"network_enabled":true,
		"cpu_limit":0.5,
		"memory_limit_mb":1024,
		"timeout_seconds":30,
		"env":{"FOO":"bar","N":1},
		"args":["-c","print(1)"]
	}`)
	got, err := coerceToolArgs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got["session_id"] != "s1" {
		t.Fatalf("session_id=%q", got["session_id"])
	}
	if got["network_enabled"] != "true" {
		t.Fatalf("network_enabled=%q", got["network_enabled"])
	}
	if got["cpu_limit"] != "0.5" {
		t.Fatalf("cpu_limit=%q", got["cpu_limit"])
	}
	if got["memory_limit_mb"] != "1024" {
		t.Fatalf("memory_limit_mb=%q", got["memory_limit_mb"])
	}
	if got["timeout_seconds"] != "30" {
		t.Fatalf("timeout_seconds=%q", got["timeout_seconds"])
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(got["env"]), &env); err != nil || env["FOO"] != "bar" {
		t.Fatalf("env=%q err=%v", got["env"], err)
	}
	var args []any
	if err := json.Unmarshal([]byte(got["args"]), &args); err != nil || len(args) != 2 {
		t.Fatalf("args=%q", got["args"])
	}
}

func TestCoerceToolArgs_InvalidJSON(t *testing.T) {
	_, err := coerceToolArgs(json.RawMessage(`{"x":`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCallTool_TypedArgsReachHandler(t *testing.T) {
	srv := newTestServer()
	_, err := srv.callTool(context.Background(), "run_command", json.RawMessage(`{
		"session_id":"s1",
		"command":"echo",
		"timeout_seconds":45,
		"async":false
	}`))
	if err != nil && strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("typed args should coerce before handler: %v", err)
	}
}

func TestSDKBridge_TypedRunCommandArgs(t *testing.T) {
	ctx := context.Background()
	sdk := buildMCPServer(newTestServer(), newTaskStore(), false)
	ct, st := mcpsdk.NewInMemoryTransports()
	if _, err := sdk.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "run_command",
		Arguments: map[string]any{
			"session_id":      "s1",
			"command":         "echo",
			"timeout_seconds": 45,
			"async":           false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// May be tool-level error (no API), but must not be JSON unmarshal failure.
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
			text = tc.Text
		}
	}
	if strings.Contains(text, "cannot unmarshal") {
		t.Fatalf("typed SDK args failed coercion: %q", text)
	}
}
