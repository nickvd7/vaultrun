package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectTestSDKSession builds the production SDK server path (bridge + tasks)
// over an in-memory transport.
func connectTestSDKSession(t *testing.T) (context.Context, *mcpsdk.ClientSession) {
	t.Helper()
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
	t.Cleanup(func() { _ = session.Close() })
	return ctx, session
}

func TestSDKProtocolInitialize(t *testing.T) {
	_, session := connectTestSDKSession(t)
	init := session.InitializeResult()
	if init == nil {
		t.Fatal("missing initialize result")
	}
	if init.ServerInfo == nil || init.ServerInfo.Name != mcpServerName {
		t.Fatalf("serverInfo=%#v", init.ServerInfo)
	}
	if init.Capabilities == nil || init.Capabilities.Tools == nil {
		t.Fatal("tools capability missing")
	}
	if init.ProtocolVersion == "" {
		t.Fatal("empty protocolVersion")
	}
	// Production clients negotiate a supported version via the SDK.
	if init.ProtocolVersion != protocolVersionCurrent && init.ProtocolVersion != protocolVersionLegacy {
		t.Fatalf("unexpected protocolVersion %q", init.ProtocolVersion)
	}
}

func TestSDKProtocolPing(t *testing.T) {
	ctx, session := connectTestSDKSession(t)
	if err := session.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSDKProtocolToolsListCatalog(t *testing.T) {
	ctx, session := connectTestSDKSession(t)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []string{
		"create_session", "list_sessions", "get_session", "delete_session",
		"run_command", "upload_file", "read_file", "list_files",
		"delete_file", "get_run", "list_runs",
		"create_snapshot", "list_snapshots",
		"create_artifact", "list_artifacts",
		"list_audit_logs",
		"list_images", "pull_image", "get_session_stats", "get_session_logs",
		"run_github_repo", "github_post_comment",
		"fs_read_file", "fs_write_file", "fs_list_dir", "fs_delete_file",
		"s3_list_buckets", "s3_list_objects", "s3_get_object",
		"s3_put_object", "s3_delete_object", "s3_head_object",
		"ssm_get_parameter", "ssm_put_parameter", "ssm_delete_parameter", "ssm_list_parameters",
		"sm_get_secret", "sm_list_secrets",
		"lambda_list_functions", "lambda_invoke",
		"sqlite_query", "sqlite_execute", "sqlite_schema",
		"pg_query", "pg_execute", "pg_schema",
		"mongo_find", "mongo_insert_one", "mongo_update", "mongo_delete",
		"mongo_aggregate", "mongo_collections", "mongo_generate_mongoose",
		"get_task", "update_task", "cancel_task",
		"verify_checkpoint",
		"memory_set", "memory_get", "memory_list", "memory_delete",
	}
	toolNames := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}
	for _, want := range wantTools {
		if !toolNames[want] {
			t.Errorf("expected tool %q in tools list", want)
		}
	}
}

func TestSDKProtocolUnknownTool(t *testing.T) {
	ctx, session := connectTestSDKSession(t)
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "nonexistent_tool",
		Arguments: map[string]any{},
	})
	if err != nil {
		// SDK may surface unknown tools as a session error; either form is OK
		// as long as the production path rejects the call.
		if !strings.Contains(err.Error(), "nonexistent_tool") && !strings.Contains(strings.ToLower(err.Error()), "unknown") && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected tool-level error, got %#v", res)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
			text = tc.Text
		}
	}
	if text != "" && !strings.Contains(text, "nonexistent_tool") {
		t.Errorf("expected tool name in error text, got %q", text)
	}
}

func TestSDKProtocolDBToolsListed(t *testing.T) {
	ctx, session := connectTestSDKSession(t)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"sqlite_query", "sqlite_execute", "sqlite_schema",
		"pg_query", "pg_execute", "pg_schema",
		"mongo_find", "mongo_insert_one", "mongo_update", "mongo_delete",
		"mongo_aggregate", "mongo_collections", "mongo_generate_mongoose",
		"get_task", "update_task", "cancel_task",
	}
	names := make(map[string]bool)
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("expected tool %q in list", w)
		}
	}
}

func TestHTTPRootAdvertisesSupportedVersions(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body struct {
		Name              string   `json:"name"`
		SupportedVersions []string `json:"supported_versions"`
		Extensions        []string `json:"extensions"`
		SDK               string   `json:"sdk"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Name != mcpServerName {
		t.Fatalf("name=%q", body.Name)
	}
	if body.SDK == "" {
		t.Fatal("missing sdk")
	}
	foundCurrent, foundLegacy := false, false
	for _, v := range body.SupportedVersions {
		if v == protocolVersionCurrent {
			foundCurrent = true
		}
		if v == protocolVersionLegacy {
			foundLegacy = true
		}
	}
	if !foundCurrent || !foundLegacy {
		t.Fatalf("supported_versions=%v", body.SupportedVersions)
	}
	hasTasks, hasApps := false, false
	for _, e := range body.Extensions {
		if e == extTasks {
			hasTasks = true
		}
		if e == extApps {
			hasApps = true
		}
	}
	if !hasTasks || !hasApps {
		t.Fatalf("extensions=%v", body.Extensions)
	}
}

func TestHTTPUnsupportedProtocolVersion(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":` + modernMeta("2099-01-01") + `}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("MCP-Protocol-Version", "2099-01-01")
	req.Header.Set("Mcp-Method", "tools/list")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	if rpcResp.Error == nil || rpcResp.Error.Code != errUnsupportedProtocolVersion {
		t.Fatalf("expected unsupported protocol version, got %+v", rpcResp.Error)
	}
}

func TestHTTPNotificationAcceptedWithoutBody(t *testing.T) {
	ts, cleanup := newTestHTTPRouter("tok")
	defer cleanup()

	// JSON-RPC notification: no id → no response body (202 Accepted).
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer tok")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) != 0 {
		t.Fatalf("expected empty body for notification, got %q", b)
	}
}
