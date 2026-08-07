package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSDKBridgeToolsList(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer()
	sdk := buildMCPServer(srv, newTaskStore(), true)

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

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 20 {
		t.Fatalf("expected many tools, got %d", len(tools.Tools))
	}
}

func TestSDKBridgeDiscover(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer()
	sdk := buildMCPServer(srv, newTaskStore(), true)

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

	// Initialize handshake (legacy path over in-memory) should expose tools capability.
	if session.InitializeResult() == nil || session.InitializeResult().Capabilities == nil {
		t.Fatal("missing initialize result capabilities")
	}
	caps := session.InitializeResult().Capabilities
	if caps.Tools == nil {
		t.Fatal("tools capability missing")
	}
	if caps.Extensions == nil || caps.Extensions[extTasks] == nil {
		t.Fatalf("tasks extension not advertised: %#v", caps.Extensions)
	}
}

func TestTasksAsyncRunCommand(t *testing.T) {
	ctx := context.Background()
	srv := newTestServer()
	store := newTaskStore()
	sdk := buildMCPServer(srv, store, false)

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
			"session_id": "s1",
			"command":    "echo",
			"async":      "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
			text = tc.Text
		}
	}
	if !strings.Contains(text, "task_") {
		t.Fatalf("expected task id in result, got %q", text)
	}

	// Extract task id and poll via custom method.
	var payload struct {
		TaskID string `json:"taskId"`
	}
	start := strings.Index(text, "{")
	if start < 0 {
		t.Fatalf("no json payload in %q", text)
	}
	if err := json.Unmarshal([]byte(text[start:]), &payload); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, ok := store.get(payload.TaskID)
		if !ok {
			t.Fatal("task missing from store")
		}
		if rec.Status != taskWorking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOAuthPRMEndpoint(t *testing.T) {
	t.Setenv("MCP_OAUTH_ISSUERS", "https://issuer.example")
	t.Setenv("MCP_PUBLIC_BASE_URL", "https://mcp.example")
	eng := buildHTTPEngine(newTestServer(), httpConfig{
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      1000,
		port:           ":8090",
	})
	ts := httptest.NewServer(eng)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PRM status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["resource"] == nil {
		t.Fatalf("missing resource in PRM: %#v", body)
	}
}

func TestAppsResourceRegistered(t *testing.T) {
	ctx := context.Background()
	sdk := buildMCPServer(newTestServer(), newTaskStore(), true)
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

	res, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: sessionPanelURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) == 0 || res.Contents[0].Text == "" {
		t.Fatal("expected HTML content")
	}
	if !strings.Contains(res.Contents[0].Text, "VaultRun") {
		t.Fatalf("unexpected content: %s", res.Contents[0].Text[:80])
	}
}
