package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func taskToolSession(t *testing.T) (context.Context, *taskStore, *mcpsdk.ClientSession) {
	t.Helper()
	ctx := context.Background()
	store := newTaskStore()
	sdk := buildMCPServer(newTestServer(), store, false)
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
	return ctx, store, session
}

func callTaskTool(t *testing.T, session *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s err: %v", name, err)
	}
	return res
}

func toolBody(res *mcpsdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestTaskTools_EdgeMissingAndUnknownIDs(t *testing.T) {
	_, _, session := taskToolSession(t)

	missing := callTaskTool(t, session, "get_task", map[string]any{})
	if !missing.IsError || !strings.Contains(toolBody(missing), "task_id") {
		t.Fatalf("missing id: %#v %q", missing.IsError, toolBody(missing))
	}

	unknown := callTaskTool(t, session, "get_task", map[string]any{"task_id": "task_" + uuid.NewString()})
	if !unknown.IsError || !strings.Contains(toolBody(unknown), "unknown") {
		t.Fatalf("unknown id: %q", toolBody(unknown))
	}

	inject := callTaskTool(t, session, "get_task", map[string]any{"task_id": "../../etc/passwd"})
	if !inject.IsError {
		t.Fatalf("injection should fail: %q", toolBody(inject))
	}

	evil := callTaskTool(t, session, "cancel_task", map[string]any{"task_id": "task_not-a-uuid"})
	if !evil.IsError {
		t.Fatalf("bad uuid form should fail: %q", toolBody(evil))
	}
}

func TestTaskTools_EdgeTaskIdCamelCaseAlias(t *testing.T) {
	ctx, store, session := taskToolSession(t)
	start := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1", "command": "echo", "async": "true", "confirm": true,
	})))
	id := extractTaskID(t, metricsToolText(start))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := callTaskTool(t, session, "get_task", map[string]any{"taskId": id})
	if got.IsError {
		t.Fatalf("camelCase taskId failed: %q", toolBody(got))
	}
	if !strings.Contains(toolBody(got), id) {
		t.Fatalf("body missing id: %q", toolBody(got))
	}
}

func TestTaskTools_EdgeTypedJSONProgressAndApprove(t *testing.T) {
	ctx, store, session := taskToolSession(t)
	start := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1", "command": "echo", "async": "true", "confirm": true,
	})))
	id := extractTaskID(t, metricsToolText(start))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Hosts often send typed JSON (number/bool), not strings.
	upd := callTaskTool(t, session, "update_task", map[string]any{
		"task_id":  id,
		"progress": 0.25,
		"message":  "waiting",
		"approve":  false, // bool, not "false"
	})
	if upd.IsError {
		t.Fatalf("typed JSON update failed (likely map[string]string bug): %q", toolBody(upd))
	}
}

func TestTaskTools_EdgeInvalidArgs(t *testing.T) {
	ctx, store, session := taskToolSession(t)
	start := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1", "command": "echo", "async": "true", "confirm": true,
	})))
	id := extractTaskID(t, metricsToolText(start))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	badProgress := callTaskTool(t, session, "update_task", map[string]any{
		"task_id": id, "progress": "nope",
	})
	if !badProgress.IsError {
		t.Fatalf("bad progress should error: %q", toolBody(badProgress))
	}

	badJSON := callTaskTool(t, session, "update_task", map[string]any{
		"task_id": id, "input_responses": "{not-json",
	})
	if !badJSON.IsError {
		t.Fatalf("bad input_responses should error: %q", toolBody(badJSON))
	}

	huge := strings.Repeat("x", maxTaskMessageRunes+10)
	bigMsg := callTaskTool(t, session, "update_task", map[string]any{
		"task_id": id, "message": huge,
	})
	if !bigMsg.IsError {
		t.Fatalf("oversized message should error: %q", toolBody(bigMsg))
	}
}

func TestTaskTools_EdgeDoubleCancelAndUpdateTerminal(t *testing.T) {
	ctx, store, session := taskToolSession(t)
	start := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1", "command": "echo", "async": "true", "confirm": true,
	})))
	id := extractTaskID(t, metricsToolText(start))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c1 := callTaskTool(t, session, "cancel_task", map[string]any{"task_id": id})
	if c1.IsError {
		t.Fatalf("first cancel: %q", toolBody(c1))
	}
	c2 := callTaskTool(t, session, "cancel_task", map[string]any{"task_id": id})
	if c2.IsError {
		t.Fatalf("second cancel should be idempotent: %q", toolBody(c2))
	}
	if !strings.Contains(toolBody(c2), "already finished") && !strings.Contains(toolBody(c2), "cancel") {
		t.Fatalf("unexpected second cancel: %q", toolBody(c2))
	}

	upd := callTaskTool(t, session, "update_task", map[string]any{
		"task_id": id, "message": "nope",
	})
	if !upd.IsError {
		t.Fatalf("update on terminal should fail: %q", toolBody(upd))
	}
}

func TestTaskTools_EdgeApproveTrueRunsTool(t *testing.T) {
	ctx, store, session := taskToolSession(t)
	start := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1", "command": "echo", "async": "true", "confirm": true,
	})))
	id := extractTaskID(t, metricsToolText(start))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	upd := callTaskTool(t, session, "update_task", map[string]any{
		"task_id": id,
		"approve": true, // bool
	})
	if upd.IsError {
		t.Fatalf("approve bool true failed: %q", toolBody(upd))
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.terminal() {
			if rec.Status != taskCompleted && rec.Status != taskFailed {
				t.Fatalf("status=%s", rec.Status)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task did not finish after approve")
}

func TestTaskTools_EdgeOwnerIsolation(t *testing.T) {
	id := "task_" + uuid.NewString()
	// Empty actor is intentionally allowed (stdio / non-OAuth). Mismatched owners are denied.
	if err := authorizeTaskAccess(&taskRecord{ID: id, Owner: "alice"}, ""); err != nil {
		t.Fatalf("empty actor should access owned task in non-OAuth mode: %v", err)
	}
	if err := authorizeTaskAccess(&taskRecord{ID: id, Owner: "alice"}, "bob"); err == nil {
		t.Fatal("bob should not access alice task")
	}
	if err := authorizeTaskAccess(&taskRecord{ID: id, Owner: "alice"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := authorizeTaskAccess(&taskRecord{ID: id, Owner: ""}, "bob"); err != nil {
		t.Fatalf("unowned task should be readable: %v", err)
	}
}

func TestTaskTools_EdgeListedAndNotAsyncable(t *testing.T) {
	ctx, _, session := taskToolSession(t)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"get_task", "update_task", "cancel_task"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	// async on control tools must not create nested tasks
	res := callTaskTool(t, session, "get_task", map[string]any{
		"task_id": "task_" + uuid.NewString(),
		"async":   "true",
	})
	if !res.IsError {
		t.Fatal("expected unknown task error, not async start")
	}
	if strings.Contains(toolBody(res), "Task started") {
		t.Fatal("get_task must not start a task")
	}
}

func TestTaskTools_EdgeProgressClampAndInputResponsesObject(t *testing.T) {
	ctx, store, session := taskToolSession(t)
	start := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1", "command": "echo", "async": "true", "confirm": true,
	})))
	id := extractTaskID(t, metricsToolText(start))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(id)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// progress as string "1.5" should clamp via update path after approve?
	// First only progress+message while input_required still allows update?
	upd := callTaskTool(t, session, "update_task", map[string]any{
		"task_id":  id,
		"progress": "1.5",
		"message":  "almost",
	})
	if upd.IsError {
		t.Fatalf("progress string update: %q", toolBody(upd))
	}
	rec, _ := store.get(id)
	if rec.Progress > 1 {
		t.Fatalf("progress not clamped: %v", rec.Progress)
	}

	// Structured input_responses as nested object (not stringified JSON)
	upd2 := callTaskTool(t, session, "update_task", map[string]any{
		"task_id": id,
		"input_responses": map[string]any{
			"confirm": map[string]any{"action": "decline"},
		},
	})
	if upd2.IsError {
		t.Fatalf("object input_responses failed: %q", toolBody(upd2))
	}
}
