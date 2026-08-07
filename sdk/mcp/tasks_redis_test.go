package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newMiniRedisStore(t *testing.T, maxInflight int) (*taskStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	if maxInflight <= 0 {
		maxInflight = 64
	}
	store := newTaskStoreWithRedis(rdb, time.Hour, 2*time.Hour, maxInflight)
	return store, mr
}

func TestRedisTaskRoundTrip(t *testing.T) {
	store, _ := newMiniRedisStore(t, 8)
	id := "task_" + uuid.NewString()
	rec := &taskRecord{
		ID:        id,
		Status:    taskWorking,
		Tool:      "run_command",
		Owner:     "alice",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Message:   "started",
	}
	if err := store.put(rec); err != nil {
		t.Fatal(err)
	}
	got, ok := store.get(id)
	if !ok {
		t.Fatal("missing")
	}
	if got.Owner != "alice" || got.Tool != "run_command" || got.Status != taskWorking {
		t.Fatalf("unexpected: %#v", got)
	}
}

func TestRedisTaskInflightCap(t *testing.T) {
	store, _ := newMiniRedisStore(t, 1)
	// Hold the single inflight slot with a non-finishing task.
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
		json.RawMessage(`{"session_id":"s1","command":"true","async":true}`))
	if !second.IsError {
		t.Fatal("expected inflight cap")
	}
}

func TestRedisTaskOwnerIsolation(t *testing.T) {
	store, _ := newMiniRedisStore(t, 8)
	id := "task_" + uuid.NewString()
	if err := store.put(&taskRecord{
		ID: id, Status: taskCompleted, Tool: "run_command", Owner: "alice",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		FinishedAt: time.Now().UTC(),
		Result:     json.RawMessage(`{"secret":"do-not-leak"}`),
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := store.get(id)
	if !ok {
		t.Fatal("missing")
	}
	if err := authorizeTaskAccess(got, "bob"); err == nil {
		t.Fatal("bob must not access alice task")
	}
	if err := authorizeTaskAccess(got, "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestRedisTaskIDInjection(t *testing.T) {
	store, mr := newMiniRedisStore(t, 8)
	// Plant a decoy key an attacker might try to read via path-like ids.
	_ = mr.Set("mcp:task:../../evil", `{"taskId":"../../evil","status":"completed","owner":"root"}`)

	for _, id := range []string{
		"../../evil",
		"mcp:task:x",
		"task_",
		"task_not-a-uuid",
		"task_\n",
		"TASK_" + uuid.NewString(),
		"",
		"task_" + strings.Repeat("a", 64),
	} {
		if _, ok := store.get(id); ok {
			t.Fatalf("get accepted invalid id %q", id)
		}
		if store.update(id, func(t *taskRecord) bool { t.Message = "x"; return true }) {
			t.Fatalf("update accepted invalid id %q", id)
		}
	}
}

func TestRedisTerminalNoTTLRefresh(t *testing.T) {
	store, mr := newMiniRedisStore(t, 8)
	id := "task_" + uuid.NewString()
	old := time.Now().UTC().Add(-time.Minute)
	if err := store.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "run_command",
		CreatedAt: old, UpdatedAt: old, Message: "started",
	}); err != nil {
		t.Fatal(err)
	}
	// Finish it.
	store.update(id, func(t *taskRecord) bool {
		t.Status = taskCompleted
		t.Message = "done"
		t.FinishedAt = time.Now().UTC()
		t.Progress = 1
		return true
	})
	key := redisTaskKey(id)
	ttl1 := mr.TTL(key)
	if ttl1 <= 0 {
		t.Fatalf("expected positive TTL after complete, got %v", ttl1)
	}
	time.Sleep(20 * time.Millisecond)
	// Terminal update must be a no-op and must not refresh TTL.
	store.update(id, func(t *taskRecord) bool {
		if t.terminal() {
			return false
		}
		t.Message = "nope"
		return true
	})
	ttl2 := mr.TTL(key)
	// miniredis TTL decreases with FastForward / real time; allow small skew but
	// refreshed TTL would jump back near store.ttl (1h).
	if ttl2 > ttl1+time.Second {
		t.Fatalf("TTL refreshed on terminal no-op: %v -> %v", ttl1, ttl2)
	}
	got, _ := store.get(id)
	if got.Message != "done" {
		t.Fatalf("message mutated: %q", got.Message)
	}
}

func TestRedisCancelPubSubCrossInstance(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)

	rdbA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdbB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdbA.Close(); _ = rdbB.Close() })

	storeA := newTaskStoreWithRedis(rdbA, time.Hour, 2*time.Hour, 8)
	storeB := newTaskStoreWithRedis(rdbB, time.Hour, 2*time.Hour, 8)

	// Give pub/sub listeners a moment to subscribe.
	time.Sleep(30 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	id := "task_" + uuid.NewString()
	if err := storeA.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "run_command",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		cancel: cancel,
	}); err != nil {
		t.Fatal(err)
	}
	storeA.storeCancel(id, cancel)

	// Cancel via store B (other "instance").
	ok := storeB.update(id, func(t *taskRecord) bool {
		if t.terminal() {
			return false
		}
		t.Status = taskCancelled
		t.Error = "cancelled"
		t.Message = "cancellation requested"
		t.Progress = 1
		t.FinishedAt = time.Now().UTC()
		return true
	})
	if !ok {
		t.Fatal("update missed")
	}
	storeB.publishCancel(id)

	deadline := time.Now().Add(2 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("cancel was not delivered to instance A")
		}
		time.Sleep(10 * time.Millisecond)
	}

	got, ok := storeB.get(id)
	if !ok || got.Status != taskCancelled {
		t.Fatalf("status=%v ok=%v", got, ok)
	}
}

func TestRedisCorruptBlobIgnored(t *testing.T) {
	store, mr := newMiniRedisStore(t, 8)
	id := "task_" + uuid.NewString()
	_ = mr.Set(redisTaskKey(id), "{not-json")
	if _, ok := store.get(id); ok {
		t.Fatal("corrupt blob must not parse as task")
	}
}

func TestRedisMaxAgeSweepsInflight(t *testing.T) {
	store, _ := newMiniRedisStore(t, 8)
	store.maxAge = time.Millisecond
	id := "task_" + uuid.NewString()
	old := time.Now().UTC().Add(-time.Hour)
	if err := store.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "run_command",
		CreatedAt: old, UpdatedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	// put() overwrote CreatedAt? No - we set old. But redisPutNew marshals our record.
	// Actually put uses the record as-is. Good.
	// Re-set createdAt via update after put because JSON roundtrip keeps it.
	store.expireRedis()
	got, ok := store.get(id)
	if !ok {
		t.Fatal("expected timed-out task retained")
	}
	if got.Status != taskFailed || got.Error != "task exceeded max age" {
		t.Fatalf("unexpected: %#v", got)
	}
}

func TestRedisFallbackWhenUnreachable(t *testing.T) {
	t.Setenv("MCP_REDIS_ADDR", "127.0.0.1:1")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_PASSWORD", "")
	store := newTaskStore()
	if store.redisEnabled() {
		t.Fatal("expected memory fallback when Redis unreachable")
	}
	id := "task_" + uuid.NewString()
	if err := store.put(&taskRecord{
		ID: id, Status: taskWorking, Tool: "x",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.get(id); !ok {
		t.Fatal("memory store should work")
	}
}

func TestValidTaskID(t *testing.T) {
	if !validTaskID("task_" + uuid.NewString()) {
		t.Fatal("uuid should be valid")
	}
	if validTaskID("task_test_1") {
		t.Fatal("non-uuid rejected")
	}
}
