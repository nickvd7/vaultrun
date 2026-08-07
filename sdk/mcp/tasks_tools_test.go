package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTaskTools_GetUpdateCancel(t *testing.T) {
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
	defer session.Close()

	start, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "run_command",
		Arguments: map[string]any{
			"session_id": "s1",
			"command":    "echo",
			"async":      "true",
			"confirm":    "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.IsError {
		t.Fatalf("start: %+v", start.Content)
	}
	text := ""
	if len(start.Content) > 0 {
		if tc, ok := start.Content[0].(*mcpsdk.TextContent); ok {
			text = tc.Text
		}
	}
	taskID := extractTaskID(t, text)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "get_task",
			Arguments: map[string]any{"task_id": taskID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.IsError {
			t.Fatalf("get_task error: %+v", got.Content)
		}
		body := ""
		if len(got.Content) > 0 {
			if tc, ok := got.Content[0].(*mcpsdk.TextContent); ok {
				body = tc.Text
			}
		}
		var st struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			t.Fatalf("parse get_task: %v body=%s", err, body)
		}
		if st.Status == "input_required" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	upd, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "update_task",
		Arguments: map[string]any{
			"task_id": taskID,
			"approve": "false",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.IsError {
		t.Fatalf("update_task: %+v", upd.Content)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(taskID)
		if ok && rec.terminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Fresh async task to cancel while working (no confirm).
	start2 := store.startToolTask(ctx, newTestServer(), "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1",
		"command":    "sleep",
		"async":      "true",
	})))
	id2 := extractTaskID(t, metricsToolText(start2))
	cancel, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "cancel_task",
		Arguments: map[string]any{"task_id": id2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancel.IsError {
		t.Fatalf("cancel_task: %+v", cancel.Content)
	}
	body := ""
	if len(cancel.Content) > 0 {
		if tc, ok := cancel.Content[0].(*mcpsdk.TextContent); ok {
			body = tc.Text
		}
	}
	if !strings.Contains(body, "cancel") && !strings.Contains(body, "finished") {
		t.Fatalf("unexpected cancel body %q", body)
	}
}
