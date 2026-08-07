package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
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
		Task struct {
			TaskID string `json:"taskId"`
		} `json:"task"`
	}
	start := strings.Index(text, "{")
	if start < 0 {
		t.Fatalf("no json payload in %q", text)
	}
	if err := json.Unmarshal([]byte(text[start:]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Task.TaskID == "" {
		t.Fatalf("missing taskId in %q", text)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, ok := store.get(payload.Task.TaskID)
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

func TestTasksUpdateAndCancel(t *testing.T) {
	store := newTaskStore()
	id := "task_" + uuid.NewString()
	if err := store.put(&taskRecord{
		ID:        id,
		Status:    taskWorking,
		Tool:      "run_command",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Message:   "started",
	}); err != nil {
		t.Fatal(err)
	}

	ok := store.update(id, func(t *taskRecord) bool {
		if t.terminal() {
			return false
		}
		t.Progress = 0.5
		t.Message = "halfway"
		return true
	})
	if !ok {
		t.Fatal("update missed")
	}
	got, found := store.get(id)
	if !found || got.Progress != 0.5 || got.Message != "halfway" || got.Status != taskWorking {
		t.Fatalf("unexpected after update: %#v", got)
	}

	store.update(id, func(t *taskRecord) bool {
		if t.terminal() {
			return false
		}
		t.Status = taskCancelled
		t.Message = "cancellation requested"
		t.Progress = 1
		t.FinishedAt = time.Now().UTC()
		return true
	})
	got, _ = store.get(id)
	if got.Status != taskCancelled {
		t.Fatalf("status=%s", got.Status)
	}

	before := got.UpdatedAt
	time.Sleep(2 * time.Millisecond)
	store.update(id, func(t *taskRecord) bool {
		if t.terminal() {
			return false
		}
		t.Progress = 0.9
		t.Message = "too late"
		return true
	})
	got, _ = store.get(id)
	if got.Progress != 1 || got.Message != "cancellation requested" {
		t.Fatalf("terminal task mutated: %#v", got)
	}
	if !got.UpdatedAt.Equal(before) {
		t.Fatalf("UpdatedAt refreshed on terminal no-op: %v -> %v", before, got.UpdatedAt)
	}
}

func TestTaskTTLExpire(t *testing.T) {
	store := &taskStore{
		tasks:       make(map[string]*taskRecord),
		cancels:     make(map[string]context.CancelFunc),
		waiters:     make(map[string]chan struct{}),
		ttl:         time.Millisecond,
		maxAge:      time.Hour,
		maxInflight: 64,
	}
	old := time.Now().UTC().Add(-time.Hour)
	oldID := "task_" + uuid.NewString()
	liveID := "task_" + uuid.NewString()
	store.tasks[oldID] = &taskRecord{
		ID:         oldID,
		Status:     taskCompleted,
		UpdatedAt:  old,
		CreatedAt:  old,
		FinishedAt: old,
	}
	store.tasks[liveID] = &taskRecord{
		ID:        liveID,
		Status:    taskWorking,
		UpdatedAt: old,
		CreatedAt: time.Now().UTC(),
	}
	store.expire()
	if _, ok := store.get(oldID); ok {
		t.Fatal("completed task should expire")
	}
	if _, ok := store.get(liveID); !ok {
		t.Fatal("working task must not expire via TTL alone")
	}
}

func TestTaskMaxAgeCancelsStaleWorking(t *testing.T) {
	store := &taskStore{
		tasks:       make(map[string]*taskRecord),
		cancels:     make(map[string]context.CancelFunc),
		waiters:     make(map[string]chan struct{}),
		ttl:         time.Hour,
		maxAge:      time.Millisecond,
		maxInflight: 64,
	}
	old := time.Now().UTC().Add(-time.Hour)
	id := "task_" + uuid.NewString()
	store.tasks[id] = &taskRecord{
		ID:        id,
		Status:    taskWorking,
		CreatedAt: old,
		UpdatedAt: old,
	}
	store.expire()
	got, ok := store.get(id)
	if !ok {
		t.Fatal("expected timed-out task retained until TTL")
	}
	if got.Status != taskFailed || got.Error != "task exceeded max age" {
		t.Fatalf("unexpected: %#v", got)
	}
}

func TestTaskMaxInflight(t *testing.T) {
	store := &taskStore{
		tasks:       make(map[string]*taskRecord),
		cancels:     make(map[string]context.CancelFunc),
		waiters:     make(map[string]chan struct{}),
		ttl:         time.Hour,
		maxAge:      time.Hour,
		maxInflight: 1,
	}
	id := "task_" + uuid.NewString()
	if err := store.put(&taskRecord{
		ID:        id,
		Status:    taskWorking,
		Tool:      "run_command",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	second := store.startToolTask(context.Background(), newTestServer(), "run_command",
		json.RawMessage(`{"session_id":"s1","command":"echo","async":true}`))
	if !second.IsError {
		t.Fatal("expected inflight cap error")
	}
}

func TestAuthorizeTaskAccess(t *testing.T) {
	rec := &taskRecord{ID: "t1", Owner: "alice"}
	if err := authorizeTaskAccess(rec, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := authorizeTaskAccess(rec, ""); err != nil {
		t.Fatal("empty actor should be allowed (stdio)")
	}
	if err := authorizeTaskAccess(rec, "bob"); err == nil {
		t.Fatal("expected denial for other owner")
	}
	if err := authorizeTaskAccess(&taskRecord{ID: "t2"}, "bob"); err != nil {
		t.Fatal("unowned task should allow any actor")
	}
}

func TestStripAsyncFlag(t *testing.T) {
	in := json.RawMessage(`{"image":"alpine","async":true,"name":"x"}`)
	out := stripAsyncFlag(in)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["async"]; ok {
		t.Fatal("async should be stripped")
	}
	if m["image"] != "alpine" || m["name"] != "x" {
		t.Fatalf("unexpected: %#v", m)
	}
	if !strings.Contains(string(in), `"async"`) {
		t.Fatal("original raw message should be unchanged")
	}
}

func TestWantsAsyncTask(t *testing.T) {
	if !wantsAsyncTask(nil, json.RawMessage(`{"async":true}`)) {
		t.Fatal("bool true")
	}
	if !wantsAsyncTask(nil, json.RawMessage(`{"async":"true"}`)) {
		t.Fatal("string true")
	}
	if wantsAsyncTask(nil, json.RawMessage(`{"async":false}`)) {
		t.Fatal("bool false")
	}
	if wantsAsyncTask(nil, json.RawMessage(`{}`)) {
		t.Fatal("missing async must not force task mode")
	}
}

func TestClampTaskMessage(t *testing.T) {
	long := strings.Repeat("ä", maxTaskMessageRunes+10)
	got := clampTaskMessage(long)
	if utf8.RuneCountInString(got) != maxTaskMessageRunes {
		t.Fatalf("got %d runes", utf8.RuneCountInString(got))
	}
}
