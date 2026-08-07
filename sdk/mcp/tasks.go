// Tasks extension (io.modelcontextprotocol/tasks) — pollable long-running work.
//
// The official Go SDK does not yet ship first-class Tasks support (still on the
// SDK roadmap). VaultRun implements the extension surface via custom methods
// so clients that advertise the extension can poll durable task handles.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type taskStatus string

const (
	taskWorking       taskStatus = "working"
	taskInputRequired taskStatus = "input_required"
	taskCompleted     taskStatus = "completed"
	taskFailed        taskStatus = "failed"
	taskCancelled     taskStatus = "cancelled"
)

const (
	defaultTaskTTL      = 30 * time.Minute
	defaultTaskCleanup  = time.Minute
	defaultTaskMaxAge   = 2 * time.Hour
	defaultMaxInflight  = 64
	maxTaskMessageRunes = 4096
	maxTaskResultBytes  = 1 << 20 // 1 MiB retained tool result
)

type taskRecord struct {
	ID           string          `json:"taskId"`
	Status       taskStatus      `json:"status"`
	Tool         string          `json:"tool"`
	Owner        string          `json:"-"` // auth UserID; empty for stdio / unbound
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	FinishedAt   time.Time       `json:"finishedAt,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        string          `json:"error,omitempty"`
	Progress     float64         `json:"progress,omitempty"` // 0..1 hint
	Message      string          `json:"message,omitempty"`
	PollInterval int             `json:"pollIntervalMs,omitempty"`
	cancel       context.CancelFunc
}

func (t *taskRecord) terminal() bool {
	switch t.Status {
	case taskCompleted, taskFailed, taskCancelled:
		return true
	default:
		return false
	}
}

type taskStore struct {
	mu          sync.RWMutex
	tasks       map[string]*taskRecord
	ttl         time.Duration
	maxAge      time.Duration
	maxInflight int
}

func newTaskStore() *taskStore {
	ttl := defaultTaskTTL
	if v := os.Getenv("MCP_TASK_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttl = time.Duration(n) * time.Second
		}
	}
	maxAge := defaultTaskMaxAge
	if v := os.Getenv("MCP_TASK_MAX_AGE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAge = time.Duration(n) * time.Second
		}
	}
	maxInflight := defaultMaxInflight
	if v := os.Getenv("MCP_TASK_MAX_INFLIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxInflight = n
		}
	}
	ts := &taskStore{
		tasks:       make(map[string]*taskRecord),
		ttl:         ttl,
		maxAge:      maxAge,
		maxInflight: maxInflight,
	}
	go ts.cleanupLoop()
	return ts
}

func (ts *taskStore) cleanupLoop() {
	ticker := time.NewTicker(defaultTaskCleanup)
	defer ticker.Stop()
	for range ticker.C {
		ts.expire()
	}
}

func (ts *taskStore) expire() {
	now := time.Now().UTC()
	ttlCutoff := now.Add(-ts.ttl)
	ageCutoff := now.Add(-ts.maxAge)

	var cancelFns []context.CancelFunc

	ts.mu.Lock()
	for id, t := range ts.tasks {
		if !t.terminal() && t.CreatedAt.Before(ageCutoff) {
			if t.cancel != nil {
				cancelFns = append(cancelFns, t.cancel)
			}
			t.Status = taskFailed
			t.Error = "task exceeded max age"
			t.Message = "timed out"
			t.Progress = 1
			t.FinishedAt = now
			t.UpdatedAt = now
			t.cancel = nil
		}
		if t.terminal() {
			finished := t.FinishedAt
			if finished.IsZero() {
				finished = t.UpdatedAt
			}
			if finished.Before(ttlCutoff) {
				delete(ts.tasks, id)
			}
		}
	}
	ts.mu.Unlock()

	for _, fn := range cancelFns {
		fn()
	}
}

func (ts *taskStore) get(id string) (*taskRecord, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	t, ok := ts.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	cp.cancel = nil
	return &cp, true
}

func (ts *taskStore) put(t *taskRecord) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tasks[t.ID] = t
}

// update applies fn under the store lock. UpdatedAt is only bumped when fn
// returns true (a real mutation). Terminal no-ops must return false so TTL
// expiry cannot be refreshed by probing tasks/update or tasks/cancel.
func (ts *taskStore) update(id string, fn func(*taskRecord) bool) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.tasks[id]
	if !ok {
		return false
	}
	if fn(t) {
		t.UpdatedAt = time.Now().UTC()
	}
	return true
}

func (ts *taskStore) inflightLocked() int {
	n := 0
	for _, t := range ts.tasks {
		if !t.terminal() {
			n++
		}
	}
	return n
}

func taskOwnerFromContext(ctx context.Context) string {
	if ti := auth.TokenInfoFromContext(ctx); ti != nil {
		return ti.UserID
	}
	return ""
}

// authorizeTaskAccess allows the call when the task has no owner (stdio), the
// caller has no identity (local/unauthenticated transport), or owners match.
// Mismatched owners get a generic "unknown" error so taskIds are not confirmed.
func authorizeTaskAccess(rec *taskRecord, actor string) error {
	if rec.Owner == "" || actor == "" || rec.Owner == actor {
		return nil
	}
	return fmt.Errorf("unknown taskId %q", rec.ID)
}

func clampTaskMessage(msg string) string {
	if utf8.RuneCountInString(msg) <= maxTaskMessageRunes {
		return msg
	}
	runes := []rune(msg)
	return string(runes[:maxTaskMessageRunes])
}

func truncateTaskResult(b []byte) json.RawMessage {
	if len(b) <= maxTaskResultBytes {
		return b
	}
	trimmed := append([]byte(nil), b[:maxTaskResultBytes]...)
	note, _ := json.Marshal(map[string]any{
		"truncated": true,
		"bytes":     len(b),
		"kept":      maxTaskResultBytes,
		"preview":   string(trimmed),
	})
	return note
}

// stripAsyncFlag removes the VaultRun async control key so tool handlers never see it.
func stripAsyncFlag(args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return args
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args
	}
	delete(m, "async")
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return b
}

func (ts *taskStore) startToolTask(ctx context.Context, srv *server, name string, args json.RawMessage) *mcpsdk.CallToolResult {
	ts.mu.Lock()
	if ts.inflightLocked() >= ts.maxInflight {
		ts.mu.Unlock()
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{
				Text: fmt.Sprintf("error: too many in-flight tasks (max %d); wait or cancel existing ones", ts.maxInflight),
			}},
			IsError: true,
		}
	}
	ts.mu.Unlock()

	id := "task_" + uuid.NewString()
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	now := time.Now().UTC()
	rec := &taskRecord{
		ID:           id,
		Status:       taskWorking,
		Tool:         name,
		Owner:        taskOwnerFromContext(ctx),
		CreatedAt:    now,
		UpdatedAt:    now,
		Progress:     0,
		Message:      "started",
		PollInterval: 2000,
		cancel:       cancel,
	}
	ts.put(rec)

	cleanArgs := stripAsyncFlag(args)
	go func() {
		defer cancel()
		result, err := srv.callTool(taskCtx, name, cleanArgs)
		ts.update(id, func(t *taskRecord) bool {
			if t.terminal() {
				return false
			}
			now := time.Now().UTC()
			if taskCtx.Err() != nil {
				t.Status = taskCancelled
				t.Error = "cancelled"
				t.Message = "cancelled"
				t.Progress = 1
				t.FinishedAt = now
				return true
			}
			if err != nil {
				t.Status = taskFailed
				t.Error = err.Error()
				t.Message = "failed"
				t.Progress = 1
				t.FinishedAt = now
				return true
			}
			b, _ := json.Marshal(result)
			t.Result = truncateTaskResult(b)
			t.Progress = 1
			t.FinishedAt = now
			if result.IsError {
				t.Status = taskFailed
				if len(result.Content) > 0 {
					t.Error = result.Content[0].Text
				} else {
					t.Error = "tool returned isError"
				}
				t.Message = "failed"
				return true
			}
			t.Status = taskCompleted
			t.Message = "completed"
			return true
		})
	}()

	payload, _ := json.Marshal(map[string]any{
		"task": map[string]any{
			"taskId":         id,
			"status":         taskWorking,
			"tool":           name,
			"pollIntervalMs": 2000,
			"progress":       0,
		},
		"hint": "Poll with tasks/get; optional tasks/update for progress notes; tasks/cancel to abort.",
	})
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{
			Text: "Task started.\n" + string(payload),
		}},
		StructuredContent: map[string]any{
			"task": map[string]any{
				"taskId": id,
				"status": string(taskWorking),
				"tool":   name,
			},
		},
	}
}

type tasksGetParams struct {
	mcpsdk.ParamsBase
	TaskID string `json:"taskId"`
}

type tasksGetResult struct {
	mcpsdk.ResultBase
	TaskID       string          `json:"taskId"`
	Status       string          `json:"status"`
	Tool         string          `json:"tool,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	Progress     float64         `json:"progress,omitempty"`
	Message      string          `json:"message,omitempty"`
	PollInterval int             `json:"pollIntervalMs,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        string          `json:"error,omitempty"`
}

type tasksCancelParams struct {
	mcpsdk.ParamsBase
	TaskID string `json:"taskId"`
}

type tasksCancelResult struct {
	mcpsdk.ResultBase
	TaskID  string `json:"taskId"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type tasksUpdateParams struct {
	mcpsdk.ParamsBase
	TaskID   string  `json:"taskId"`
	Message  string  `json:"message,omitempty"`
	Progress float64 `json:"progress,omitempty"`
	// Input is reserved for future input_required flows (MRTR).
	Input json.RawMessage `json:"input,omitempty"`
}

type tasksUpdateResult struct {
	mcpsdk.ResultBase
	TaskID   string  `json:"taskId"`
	Status   string  `json:"status"`
	Message  string  `json:"message,omitempty"`
	Progress float64 `json:"progress,omitempty"`
}

func registerTaskMethods(sdk *mcpsdk.Server, tasks *taskStore) error {
	if err := mcpsdk.AddReceivingCustomMethod(sdk, "tasks/get",
		func(ctx context.Context, ss *mcpsdk.ServerSession, params *tasksGetParams) (*tasksGetResult, error) {
			if params == nil || params.TaskID == "" {
				return nil, fmt.Errorf("taskId is required")
			}
			rec, ok := tasks.get(params.TaskID)
			if !ok {
				return nil, fmt.Errorf("unknown taskId %q", params.TaskID)
			}
			if err := authorizeTaskAccess(rec, taskOwnerFromContext(ctx)); err != nil {
				return nil, err
			}
			return &tasksGetResult{
				TaskID:       rec.ID,
				Status:       string(rec.Status),
				Tool:         rec.Tool,
				CreatedAt:    rec.CreatedAt,
				UpdatedAt:    rec.UpdatedAt,
				Progress:     rec.Progress,
				Message:      rec.Message,
				PollInterval: rec.PollInterval,
				Result:       rec.Result,
				Error:        rec.Error,
			}, nil
		}); err != nil {
		return err
	}

	if err := mcpsdk.AddReceivingCustomMethod(sdk, "tasks/cancel",
		func(ctx context.Context, ss *mcpsdk.ServerSession, params *tasksCancelParams) (*tasksCancelResult, error) {
			if params == nil || params.TaskID == "" {
				return nil, fmt.Errorf("taskId is required")
			}
			actor := taskOwnerFromContext(ctx)
			if rec, ok := tasks.get(params.TaskID); ok {
				if err := authorizeTaskAccess(rec, actor); err != nil {
					return nil, err
				}
			}

			var cancelFn context.CancelFunc
			var alreadyTerminal bool
			var statusAfter string
			ok := tasks.update(params.TaskID, func(t *taskRecord) bool {
				if t.terminal() {
					alreadyTerminal = true
					statusAfter = string(t.Status)
					return false // do not refresh TTL
				}
				cancelFn = t.cancel
				t.cancel = nil
				t.Status = taskCancelled
				t.Error = "cancelled"
				t.Message = "cancellation requested"
				t.Progress = 1
				t.FinishedAt = time.Now().UTC()
				statusAfter = string(taskCancelled)
				return true
			})
			if !ok {
				return nil, fmt.Errorf("unknown taskId %q", params.TaskID)
			}
			if cancelFn != nil {
				cancelFn()
			}
			msg := "cancellation requested"
			if alreadyTerminal {
				msg = "task already finished"
			}
			return &tasksCancelResult{TaskID: params.TaskID, Status: statusAfter, Message: msg}, nil
		}); err != nil {
		return err
	}

	if err := mcpsdk.AddReceivingCustomMethod(sdk, "tasks/update",
		func(ctx context.Context, ss *mcpsdk.ServerSession, params *tasksUpdateParams) (*tasksUpdateResult, error) {
			if params == nil || params.TaskID == "" {
				return nil, fmt.Errorf("taskId is required")
			}
			if utf8.RuneCountInString(params.Message) > maxTaskMessageRunes {
				return nil, fmt.Errorf("message exceeds %d character limit", maxTaskMessageRunes)
			}
			if len(params.Input) > maxTaskMessageRunes {
				return nil, fmt.Errorf("input exceeds %d byte limit", maxTaskMessageRunes)
			}
			actor := taskOwnerFromContext(ctx)
			if rec, ok := tasks.get(params.TaskID); ok {
				if err := authorizeTaskAccess(rec, actor); err != nil {
					return nil, err
				}
			}

			var terminal bool
			ok := tasks.update(params.TaskID, func(t *taskRecord) bool {
				if t.terminal() {
					terminal = true
					return false // do not refresh TTL
				}
				changed := false
				if params.Message != "" {
					t.Message = clampTaskMessage(params.Message)
					changed = true
				}
				if params.Progress > 0 {
					p := params.Progress
					if p > 1 {
						p = 1
					}
					if t.Progress != p {
						t.Progress = p
						changed = true
					}
				}
				// input_required → working when client supplies input
				if len(params.Input) > 0 && t.Status == taskInputRequired {
					t.Status = taskWorking
					t.Message = "input received"
					changed = true
				}
				return changed
			})
			if !ok {
				return nil, fmt.Errorf("unknown taskId %q", params.TaskID)
			}
			if terminal {
				return nil, fmt.Errorf("task %q is already finished", params.TaskID)
			}
			rec, _ := tasks.get(params.TaskID)
			return &tasksUpdateResult{
				TaskID:   params.TaskID,
				Status:   string(rec.Status),
				Message:  rec.Message,
				Progress: rec.Progress,
			}, nil
		}); err != nil {
		return err
	}

	slog.Debug("vaultrun-mcp: tasks extension methods registered",
		"methods", []string{"tasks/get", "tasks/cancel", "tasks/update"})
	return nil
}
