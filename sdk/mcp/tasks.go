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
	"sync"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type taskStatus string

const (
	taskWorking   taskStatus = "working"
	taskCompleted taskStatus = "completed"
	taskFailed    taskStatus = "failed"
	taskCancelled taskStatus = "cancelled"
)

type taskRecord struct {
	ID        string          `json:"taskId"`
	Status    taskStatus      `json:"status"`
	Tool      string          `json:"tool"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	cancel    context.CancelFunc
}

type taskStore struct {
	mu    sync.RWMutex
	tasks map[string]*taskRecord
}

func newTaskStore() *taskStore {
	return &taskStore{tasks: make(map[string]*taskRecord)}
}

func (ts *taskStore) get(id string) (*taskRecord, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	t, ok := ts.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
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

func (ts *taskStore) startToolTask(ctx context.Context, srv *server, name string, args json.RawMessage) *mcpsdk.CallToolResult {
	id := "task_" + uuid.NewString()
	taskCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	rec := &taskRecord{
		ID:        id,
		Status:    taskWorking,
		Tool:      name,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		cancel:    cancel,
	}
	ts.put(rec)

	go func() {
		defer cancel()
		result, err := srv.callTool(taskCtx, name, args)
		ts.update(id, func(t *taskRecord) {
			if taskCtx.Err() != nil {
				t.Status = taskCancelled
				t.Error = "cancelled"
				return
			}
			if err != nil {
				t.Status = taskFailed
				t.Error = err.Error()
				return
			}
			b, _ := json.Marshal(result)
			t.Result = b
			if result.IsError {
				t.Status = taskFailed
				if len(result.Content) > 0 {
					t.Error = result.Content[0].Text
				} else {
					t.Error = "tool returned isError"
				}
				return
			}
			t.Status = taskCompleted
		})
	}()

	payload, _ := json.Marshal(map[string]any{
		"taskId":         id,
		"status":         taskWorking,
		"tool":           name,
		"pollIntervalMs": 2000,
		"hint":           "Poll with tasks/get until status is completed, failed, or cancelled.",
	})
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{
			Text: "Task started.\n" + string(payload),
		}},
		StructuredContent: map[string]any{
			"taskId": id,
			"status": string(taskWorking),
			"tool":   name,
		},
	}
}

type tasksGetParams struct {
	mcpsdk.ParamsBase
	TaskID string `json:"taskId"`
}

type tasksGetResult struct {
	mcpsdk.ResultBase
	TaskID    string          `json:"taskId"`
	Status    string          `json:"status"`
	Tool      string          `json:"tool,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
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
				TaskID:    rec.ID,
				Status:    string(rec.Status),
				Tool:      rec.Tool,
				CreatedAt: rec.CreatedAt,
				UpdatedAt: rec.UpdatedAt,
				Result:    rec.Result,
				Error:     rec.Error,
			}, nil
		}); err != nil {
		return err
	}

	if err := mcpsdk.AddReceivingCustomMethod(sdk, "tasks/cancel",
		func(ctx context.Context, ss *mcpsdk.ServerSession, params *tasksCancelParams) (*tasksCancelResult, error) {
			if params == nil || params.TaskID == "" {
				return nil, fmt.Errorf("taskId is required")
			}
			ts := tasks
			var cancelled bool
			ok := ts.update(params.TaskID, func(t *taskRecord) {
				if t.Status == taskWorking && t.cancel != nil {
					t.cancel()
					t.Status = taskCancelled
					t.Error = "cancelled"
					cancelled = true
				}
			})
			if !ok {
				return nil, fmt.Errorf("unknown taskId %q", params.TaskID)
			}
			msg := "task already finished"
			status := ""
			if rec, ok := ts.get(params.TaskID); ok {
				status = string(rec.Status)
			}
			if cancelled {
				msg = "cancellation requested"
			}
			return &tasksCancelResult{TaskID: params.TaskID, Status: status, Message: msg}, nil
		}); err != nil {
		return err
	}

	return nil
}
