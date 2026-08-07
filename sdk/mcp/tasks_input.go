package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	maxInputRequestKeys   = 8
	maxInputPayloadBytes  = 16 << 10 // 16 KiB per request/response value
	redisTaskInputChan    = "mcp:tasks:input"
	taskConfirmRequestKey = "confirm"
)

// taskInputRequest is one outstanding server→client request (typically elicitation/create).
type taskInputRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// taskInputResponse is the client's answer for one inputRequests key.
type taskInputResponse struct {
	Action  string          `json:"action,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func approvalElicitation(message string) map[string]taskInputRequest {
	params, _ := json.Marshal(map[string]any{
		"mode":    "form",
		"message": message,
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"approved": map[string]any{
					"type":        "boolean",
					"description": "Whether to proceed with the operation.",
				},
			},
			"required": []string{"approved"},
		},
	})
	return map[string]taskInputRequest{
		taskConfirmRequestKey: {
			Method: "elicitation/create",
			Params: params,
		},
	}
}

func cloneInputRequests(in map[string]taskInputRequest) map[string]taskInputRequest {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]taskInputRequest, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (ts *taskStore) registerWaiter(id string) <-chan struct{} {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if existing, ok := ts.waiters[id]; ok {
		return existing
	}
	ch := make(chan struct{})
	ts.waiters[id] = ch
	return ch
}

func (ts *taskStore) signalWaiters(id string) {
	ts.mu.Lock()
	ch := ts.waiters[id]
	delete(ts.waiters, id)
	ts.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// requestInput moves a working task to input_required with the given requests.
// Keys must be unique for the lifetime of the task (caller responsibility).
func (ts *taskStore) requestInput(id string, requests map[string]taskInputRequest) error {
	if !validTaskID(id) {
		return fmt.Errorf("unknown taskId %q", id)
	}
	if len(requests) == 0 {
		return fmt.Errorf("inputRequests required")
	}
	if len(requests) > maxInputRequestKeys {
		return fmt.Errorf("too many inputRequests (max %d)", maxInputRequestKeys)
	}
	for k, v := range requests {
		if k == "" || len(k) > 64 {
			return fmt.Errorf("invalid inputRequests key")
		}
		if len(v.Params) > maxInputPayloadBytes {
			return fmt.Errorf("inputRequests[%q] exceeds size limit", k)
		}
	}

	ok := ts.update(id, func(t *taskRecord) bool {
		if t.terminal() || t.Status == taskCancelled {
			return false
		}
		if t.InputRequests == nil {
			t.InputRequests = make(map[string]taskInputRequest)
		}
		if t.UsedInputKeys == nil {
			t.UsedInputKeys = make(map[string]bool)
		}
		for k, req := range requests {
			if t.UsedInputKeys[k] {
				continue // never reuse keys
			}
			if _, outstanding := t.InputRequests[k]; outstanding {
				continue
			}
			t.InputRequests[k] = req
			t.UsedInputKeys[k] = true
		}
		t.Status = taskInputRequired
		t.Message = "waiting for input"
		return true
	})
	if !ok {
		return fmt.Errorf("unknown taskId %q", id)
	}
	ts.signalWaiters(id)
	ts.publishInput(id)
	return nil
}

// applyInputResponses merges client responses. Unknown/already-satisfied keys are ignored.
// When all outstanding requests are fulfilled the task returns to working.
func (ts *taskStore) applyInputResponses(id string, responses map[string]taskInputResponse) (allSatisfied bool, err error) {
	if !validTaskID(id) {
		return false, fmt.Errorf("unknown taskId %q", id)
	}
	if len(responses) > maxInputRequestKeys {
		return false, fmt.Errorf("too many inputResponses (max %d)", maxInputRequestKeys)
	}
	for k, v := range responses {
		if len(k) > 64 {
			return false, fmt.Errorf("invalid inputResponses key")
		}
		if len(v.Content)+len(v.Error)+len(v.Action) > maxInputPayloadBytes {
			return false, fmt.Errorf("inputResponses[%q] exceeds size limit", k)
		}
	}

	var terminal bool
	ok := ts.update(id, func(t *taskRecord) bool {
		if t.terminal() {
			terminal = true
			return false
		}
		if t.Status != taskInputRequired || len(t.InputRequests) == 0 {
			return false
		}
		if t.InputResponses == nil {
			t.InputResponses = make(map[string]taskInputResponse)
		}
		changed := false
		for k, resp := range responses {
			if _, outstanding := t.InputRequests[k]; !outstanding {
				continue // ignore unknown / already answered
			}
			t.InputResponses[k] = resp
			delete(t.InputRequests, k)
			changed = true
		}
		if !changed {
			return false
		}
		if len(t.InputRequests) == 0 {
			t.Status = taskWorking
			t.Message = "input received"
			allSatisfied = true
		} else {
			t.Message = "waiting for remaining input"
		}
		return true
	})
	if !ok {
		return false, fmt.Errorf("unknown taskId %q", id)
	}
	if terminal {
		return false, fmt.Errorf("task %q is already finished", id)
	}
	if allSatisfied {
		ts.signalWaiters(id)
		ts.publishInput(id)
	}
	return allSatisfied, nil
}

// waitForInput blocks until the task leaves input_required with no outstanding
// requests, or until ctx is cancelled / the task becomes terminal.
func (ts *taskStore) waitForInput(ctx context.Context, id string) (map[string]taskInputResponse, error) {
	for {
		rec, ok := ts.get(id)
		if !ok {
			return nil, fmt.Errorf("unknown taskId %q", id)
		}
		if rec.Status == taskCancelled || rec.Status == taskFailed || rec.Status == taskCompleted {
			if rec.Status == taskCancelled {
				return nil, context.Canceled
			}
			return nil, fmt.Errorf("task %s", rec.Status)
		}
		if rec.Status == taskWorking && len(rec.InputRequests) == 0 {
			return rec.InputResponses, nil
		}
		if rec.Status != taskInputRequired {
			return rec.InputResponses, nil
		}

		ch := ts.registerWaiter(id)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
			continue
		case <-time.After(250 * time.Millisecond):
			// Poll — covers Redis updates from another instance without a local signal.
			continue
		}
	}
}

func responseApproved(resp taskInputResponse) bool {
	if resp.Action == "decline" || resp.Action == "cancel" {
		return false
	}
	if len(resp.Content) == 0 {
		// Accept action with empty content still counts as approval for simple confirms.
		return resp.Action == "accept" || resp.Action == ""
	}
	var content struct {
		Approved *bool `json:"approved"`
	}
	if err := json.Unmarshal(resp.Content, &content); err != nil {
		return false
	}
	if content.Approved != nil {
		return *content.Approved
	}
	return resp.Action == "accept"
}

func wantsConfirm(args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return false
	}
	switch v := m["confirm"].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	default:
		return false
	}
}

// stripTaskControlFlags removes VaultRun control keys so tool handlers never see them.
func stripTaskControlFlags(args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return args
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return args
	}
	delete(m, "async")
	delete(m, "confirm")
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return b
}

func (ts *taskStore) publishInput(id string) {
	if !ts.redisEnabled() || !validTaskID(id) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = ts.rdb.Publish(ctx, redisTaskInputChan, id).Err()
}
