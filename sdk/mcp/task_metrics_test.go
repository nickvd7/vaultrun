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
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestTaskMetrics_ObserveLifecycle(t *testing.T) {
	store := newTaskStore()
	beforeStarted := testutil.ToFloat64(mcpTasksStarted.WithLabelValues("run_command", "memory"))
	beforeInflight := testutil.ToFloat64(mcpTasksInflight.WithLabelValues("memory"))
	beforeFailed := testutil.ToFloat64(mcpTasksTerminal.WithLabelValues("failed", "memory"))
	beforeCompleted := testutil.ToFloat64(mcpTasksTerminal.WithLabelValues("completed", "memory"))

	srv := newTestServer()
	res := store.startToolTask(context.Background(), srv, "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1",
		"command":    "echo",
		"async":      "true",
	})))
	if res.IsError {
		t.Fatalf("start error: %+v", res.Content)
	}
	if got := testutil.ToFloat64(mcpTasksStarted.WithLabelValues("run_command", "memory")); got != beforeStarted+1 {
		t.Fatalf("started: got %v want %v", got, beforeStarted+1)
	}

	taskID := extractTaskID(t, metricsToolText(res))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(taskID)
		if ok && rec.terminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec, ok := store.get(taskID)
	if !ok {
		t.Fatal("task missing")
	}
	if !rec.terminal() {
		t.Fatalf("status=%s", rec.Status)
	}
	switch rec.Status {
	case taskCompleted:
		if got := testutil.ToFloat64(mcpTasksTerminal.WithLabelValues("completed", "memory")); got != beforeCompleted+1 {
			t.Fatalf("terminal completed: got %v want %v", got, beforeCompleted+1)
		}
	case taskFailed:
		if got := testutil.ToFloat64(mcpTasksTerminal.WithLabelValues("failed", "memory")); got != beforeFailed+1 {
			t.Fatalf("terminal failed: got %v want %v", got, beforeFailed+1)
		}
	default:
		t.Fatalf("unexpected terminal status %s", rec.Status)
	}
	if got := testutil.ToFloat64(mcpTasksInflight.WithLabelValues("memory")); got != beforeInflight {
		t.Fatalf("inflight should return to baseline, got %v want %v", got, beforeInflight)
	}
}

func TestTaskMetrics_InputRequired(t *testing.T) {
	store := newTaskStore()
	before := testutil.ToFloat64(mcpTasksInputRequired)
	beforeInflight := testutil.ToFloat64(mcpTasksInflight.WithLabelValues("memory"))
	srv := newTestServer()
	res := store.startToolTask(context.Background(), srv, "run_command", json.RawMessage(mustJSON(map[string]any{
		"session_id": "s1",
		"command":    "echo",
		"async":      "true",
		"confirm":    true,
	})))
	if res.IsError {
		t.Fatalf("start error: %+v", res.Content)
	}
	taskID := extractTaskID(t, metricsToolText(res))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(taskID)
		if ok && rec.Status == taskInputRequired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, ok := store.get(taskID)
	if !ok || rec.Status != taskInputRequired {
		t.Fatalf("expected input_required, got %#v", rec)
	}
	if got := testutil.ToFloat64(mcpTasksInputRequired); got != before+1 {
		t.Fatalf("input_required: got %v want %v", got, before+1)
	}
	if got := testutil.ToFloat64(mcpTasksInflight.WithLabelValues("memory")); got != beforeInflight+1 {
		t.Fatalf("inflight while input_required: got %v want %v", got, beforeInflight+1)
	}
	_, _ = store.applyInputResponses(taskID, map[string]taskInputResponse{
		taskConfirmRequestKey: {Action: "decline"},
	})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.get(taskID)
		if ok && rec.terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := testutil.ToFloat64(mcpTasksInflight.WithLabelValues("memory")); got != beforeInflight {
		t.Fatalf("inflight after decline: got %v want %v", got, beforeInflight)
	}
}

func TestHTTPMetricsEndpoint(t *testing.T) {
	eng := buildHTTPEngine(newTestServer(), httpConfig{
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      1000,
		port:           ":8099",
	})
	ts := httptest.NewServer(eng)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestHTTPMetricsEndpointAuth(t *testing.T) {
	t.Setenv("MCP_METRICS_TOKEN", "metrics-secret")
	eng := buildHTTPEngine(newTestServer(), httpConfig{
		authToken:      "tok",
		allowedOrigins: []string{"*"},
		rateLimit:      1000,
		port:           ":8098",
	})
	ts := httptest.NewServer(eng)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer metrics-secret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("authed status %d", resp2.StatusCode)
	}
}

func metricsToolText(res *mcpsdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

func extractTaskID(t *testing.T, text string) string {
	t.Helper()
	var payload struct {
		Task struct {
			TaskID string `json:"taskId"`
		} `json:"task"`
	}
	start := strings.Index(text, "{")
	if start < 0 {
		t.Fatalf("no json in %q", text)
	}
	if err := json.Unmarshal([]byte(text[start:]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Task.TaskID == "" {
		t.Fatalf("missing taskId in %q", text)
	}
	return payload.Task.TaskID
}
