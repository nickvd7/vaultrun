package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTaskInputRequiredApprove(t *testing.T) {
	store := newTaskStore()
	id := "task_" + uuid.NewString()
	if err := store.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "run_command",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan map[string]taskInputResponse, 1)
	go func() {
		resp, err := store.waitForInput(context.Background(), id)
		if err != nil {
			t.Errorf("wait: %v", err)
			done <- nil
			return
		}
		done <- resp
	}()

	time.Sleep(20 * time.Millisecond)
	if err := store.requestInput(id, approvalElicitation("Approve?")); err != nil {
		t.Fatal(err)
	}
	got, ok := store.get(id)
	if !ok || got.Status != taskInputRequired || got.InputRequests[taskConfirmRequestKey].Method != "elicitation/create" {
		t.Fatalf("expected input_required with elicitation: %#v", got)
	}

	content, _ := json.Marshal(map[string]any{"approved": true})
	if _, err := store.applyInputResponses(id, map[string]taskInputResponse{
		taskConfirmRequestKey: {Action: "accept", Content: content},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.get(id)
	if got.Status != taskWorking || len(got.InputRequests) != 0 {
		t.Fatalf("expected working after approve: %#v", got)
	}

	select {
	case resp := <-done:
		if !responseApproved(resp[taskConfirmRequestKey]) {
			t.Fatal("waiter did not see approval")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter timeout")
	}
}

func TestTaskInputRequiredDecline(t *testing.T) {
	if responseApproved(taskInputResponse{Action: "decline"}) {
		t.Fatal("decline must not approve")
	}
	content, _ := json.Marshal(map[string]any{"approved": false})
	if responseApproved(taskInputResponse{Action: "accept", Content: content}) {
		t.Fatal("approved:false must not approve")
	}
}

func TestTaskInputIgnoresUnknownKeys(t *testing.T) {
	store := newTaskStore()
	id := "task_" + uuid.NewString()
	_ = store.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "x",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	_ = store.requestInput(id, approvalElicitation("x"))
	_, err := store.applyInputResponses(id, map[string]taskInputResponse{
		"nope": {Action: "accept"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := store.get(id)
	if got.Status != taskInputRequired {
		t.Fatalf("unknown key must not satisfy: %s", got.Status)
	}
}

func TestTaskInputRejectsOversized(t *testing.T) {
	store := newTaskStore()
	id := "task_" + uuid.NewString()
	_ = store.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "x",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	big := make([]byte, maxInputPayloadBytes+10)
	for i := range big {
		big[i] = 'a'
	}
	err := store.requestInput(id, map[string]taskInputRequest{
		"k": {Method: "elicitation/create", Params: big},
	})
	if err == nil {
		t.Fatal("expected size error")
	}
}

func TestTaskConfirmEndToEnd(t *testing.T) {
	store := newTaskStore()
	srv := newTestServer()
	res := store.startToolTask(context.Background(), srv, "run_command",
		json.RawMessage(`{"session_id":"s1","command":"echo","async":true,"confirm":true}`))
	if res.IsError {
		t.Fatalf("%+v", res.Content)
	}

	sc, _ := res.StructuredContent.(map[string]any)
	task, _ := sc["task"].(map[string]any)
	id, _ := task["taskId"].(string)
	if !validTaskID(id) {
		t.Fatalf("bad id %#v", sc)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, ok := store.get(id)
		if ok && got.Status == taskInputRequired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never reached input_required: %#v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}

	content, _ := json.Marshal(map[string]any{"approved": true})
	if _, err := store.applyInputResponses(id, map[string]taskInputResponse{
		taskConfirmRequestKey: {Action: "accept", Content: content},
	}); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		got, ok := store.get(id)
		if !ok {
			t.Fatal("missing")
		}
		if got.Status == taskCompleted || got.Status == taskFailed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stuck in %#v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWantsConfirm(t *testing.T) {
	if !wantsConfirm(json.RawMessage(`{"confirm":true}`)) {
		t.Fatal("bool")
	}
	if !wantsConfirm(json.RawMessage(`{"confirm":"true"}`)) {
		t.Fatal("string")
	}
	if wantsConfirm(json.RawMessage(`{}`)) {
		t.Fatal("missing")
	}
}

func TestStripTaskControlFlags(t *testing.T) {
	out := stripTaskControlFlags(json.RawMessage(`{"async":true,"confirm":true,"command":"x"}`))
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if _, ok := m["async"]; ok {
		t.Fatal("async")
	}
	if _, ok := m["confirm"]; ok {
		t.Fatal("confirm")
	}
	if m["command"] != "x" {
		t.Fatalf("%#v", m)
	}
}
