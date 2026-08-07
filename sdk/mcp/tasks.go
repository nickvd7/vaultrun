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

	"github.com/google/uuid"
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
	defaultTaskTTL     = 30 * time.Minute
	defaultTaskCleanup = time.Minute
)

type taskRecord struct {
	ID           string          `json:"taskId"`
	Status       taskStatus      `json:"status"`
	Tool         string          `json:"tool"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
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
	mu    sync.RWMutex
	tasks map[string]*taskRecord
	ttl   time.Duration
}

func newTaskStore() *taskStore {
	ttl := defaultTaskTTL
	if v := os.Getenv("MCP_TASK_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttl = time.Duration(n) * time.Second
		}
	}
	ts := &taskStore{tasks: make(map[string]*taskRecord), ttl: ttl}
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
	cutoff := time.Now().UTC().Add(-ts.ttl)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for id, t := range ts.tasks {
		if t.terminal() && t.UpdatedAt.Before(cutoff) {
			delete(ts.tasks, id)
		}
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

func (ts *taskStore) update(id string, fn func(*taskRecord)) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.tasks[id]
	if !ok {
		return false
	}
	fn(t)
	t.UpdatedAt = time.Now().UTC()
	return true
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
	id := "task_" + uuid.NewString()
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	rec := &taskRecord{
		ID:           id,
		Status:       taskWorking,
		Tool:         name,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
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
		ts.update(id, func(t *taskRecord) {
			if t.Status == taskCancelled {
				return
			}
			if taskCtx.Err() != nil {
				t.Status = taskCancelled
				t.Error = "cancelled"
				t.Message = "cancelled"
				t.Progress = 1
				return
			}
			if err != nil {
				t.Status = taskFailed
				t.Error = err.Error()
				t.Message = "failed"
				t.Progress = 1
				return
			}
			b, _ := json.Marshal(result)
			t.Result = b
			t.Progress = 1
			if result.IsError {
				t.Status = taskFailed
				if len(result.Content) > 0 {
					t.Error = result.Content[0].Text
				} else {
					t.Error = "tool returned isError"
				}
				t.Message = "failed"
				return
			}
			t.Status = taskCompleted
			t.Message = "completed"
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
			var cancelFn context.CancelFunc
			var alreadyTerminal bool
			ok := tasks.update(params.TaskID, func(t *taskRecord) {
				if t.terminal() {
					alreadyTerminal = true
					return
				}
				cancelFn = t.cancel
				t.Status = taskCancelled
				t.Error = "cancelled"
				t.Message = "cancellation requested"
				t.Progress = 1
			})
			if !ok {
				return nil, fmt.Errorf("unknown taskId %q", params.TaskID)
			}
			if cancelFn != nil {
				cancelFn()
			}
			status := string(taskCancelled)
			msg := "cancellation requested"
			if alreadyTerminal {
				if rec, ok := tasks.get(params.TaskID); ok {
					status = string(rec.Status)
				}
				msg = "task already finished"
			}
			return &tasksCancelResult{TaskID: params.TaskID, Status: status, Message: msg}, nil
		}); err != nil {
		return err
	}

	if err := mcpsdk.AddReceivingCustomMethod(sdk, "tasks/update",
		func(ctx context.Context, ss *mcpsdk.ServerSession, params *tasksUpdateParams) (*tasksUpdateResult, error) {
			if params == nil || params.TaskID == "" {
				return nil, fmt.Errorf("taskId is required")
			}
			var terminal bool
			ok := tasks.update(params.TaskID, func(t *taskRecord) {
				if t.terminal() {
					terminal = true
					return
				}
				if params.Message != "" {
					t.Message = params.Message
				}
				if params.Progress > 0 {
					if params.Progress > 1 {
						t.Progress = 1
					} else {
						t.Progress = params.Progress
					}
				}
				// input_required → working when client supplies input
				if len(params.Input) > 0 && t.Status == taskInputRequired {
					t.Status = taskWorking
					t.Message = "input received"
				}
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
