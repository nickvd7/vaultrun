package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisTaskKeyPrefix   = "mcp:task:"
	redisTaskInflightKey = "mcp:tasks:inflight"
	redisTaskCancelChan  = "mcp:tasks:cancel"
)

func redisTaskKey(id string) string {
	return redisTaskKeyPrefix + id
}

// claimAndSetTask atomically enforces the inflight cap and writes the task blob.
//
// KEYS[1] = inflight SET · KEYS[2] = task key
// ARGV[1] = max inflight · ARGV[2] = task JSON · ARGV[3] = task id · ARGV[4] = working TTL seconds
// Returns 1 on success, 0 if cap exceeded.
var claimAndSetTask = redis.NewScript(`
if redis.call('SCARD', KEYS[1]) >= tonumber(ARGV[1]) then
  return 0
end
redis.call('SADD', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[2], 'EX', tonumber(ARGV[4]))
return 1
`)

// casSetTask updates a task only when the stored JSON still matches oldJSON (optimistic lock).
// On terminal transition, removes from inflight and applies finished TTL.
//
// KEYS[1] = task key · KEYS[2] = inflight SET
// ARGV[1] = old JSON · ARGV[2] = new JSON · ARGV[3] = task id
// ARGV[4] = "1" if terminal · ARGV[5] = TTL seconds when terminal (else working TTL)
// Returns 1 ok, 0 conflict/missing, -1 if old missing.
var casSetTask = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if not cur then
  return -1
end
if cur ~= ARGV[1] then
  return 0
end
local ttl = tonumber(ARGV[5])
redis.call('SET', KEYS[1], ARGV[2], 'EX', ttl)
if ARGV[4] == '1' then
  redis.call('SREM', KEYS[2], ARGV[3])
end
return 1
`)

func (ts *taskStore) redisGet(id string) (*taskRecord, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, err := ts.rdb.Get(ctx, redisTaskKey(id)).Bytes()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		slog.Warn("vaultrun-mcp: redis task get failed", "taskId", id, "err", err)
		return nil, false
	}
	var rec taskRecord
	if err := json.Unmarshal(val, &rec); err != nil {
		slog.Warn("vaultrun-mcp: redis task corrupt", "taskId", id, "err", err)
		return nil, false
	}
	rec.cancel = nil
	return &rec, true
}

func (ts *taskStore) redisPutNew(t *taskRecord) error {
	cp := *t
	cp.cancel = nil
	blob, err := json.Marshal(&cp)
	if err != nil {
		return err
	}
	workingTTL := int(ts.maxAge.Seconds())
	if workingTTL < 60 {
		workingTTL = 60
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	claimed, err := claimAndSetTask.Run(ctx, ts.rdb,
		[]string{redisTaskInflightKey, redisTaskKey(t.ID)},
		ts.maxInflight, string(blob), t.ID, workingTTL,
	).Int()
	if err != nil {
		return fmt.Errorf("redis claim task: %w", err)
	}
	if claimed == 0 {
		return errTaskInflightFull
	}
	return nil
}

func (ts *taskStore) redisUpdate(id string, fn func(*taskRecord) bool) bool {
	const maxAttempts = 8
	for attempt := 0; attempt < maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		oldBlob, err := ts.rdb.Get(ctx, redisTaskKey(id)).Bytes()
		cancel()
		if err == redis.Nil {
			return false
		}
		if err != nil {
			slog.Warn("vaultrun-mcp: redis task update get failed", "taskId", id, "err", err)
			return false
		}
		var rec taskRecord
		if err := json.Unmarshal(oldBlob, &rec); err != nil {
			slog.Warn("vaultrun-mcp: redis task update corrupt", "taskId", id, "err", err)
			return false
		}
		wasTerminal := rec.terminal()
		mutated := fn(&rec)
		if !mutated {
			// No-op (e.g. terminal probe): do not rewrite key / refresh TTL.
			return true
		}
		rec.UpdatedAt = time.Now().UTC()
		rec.cancel = nil
		newBlob, err := json.Marshal(&rec)
		if err != nil {
			return false
		}
		ttlSec := int(ts.maxAge.Seconds())
		terminalFlag := "0"
		if rec.terminal() {
			terminalFlag = "1"
			ttlSec = int(ts.ttl.Seconds())
			if ttlSec < 1 {
				ttlSec = 1
			}
		} else if ttlSec < 60 {
			ttlSec = 60
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		res, err := casSetTask.Run(ctx2, ts.rdb,
			[]string{redisTaskKey(id), redisTaskInflightKey},
			string(oldBlob), string(newBlob), id, terminalFlag, ttlSec,
		).Int()
		cancel2()
		if err != nil {
			slog.Warn("vaultrun-mcp: redis task cas failed", "taskId", id, "err", err)
			return false
		}
		if res == 1 {
			_ = wasTerminal
			return true
		}
		if res == -1 {
			return false
		}
		// conflict — retry
	}
	slog.Warn("vaultrun-mcp: redis task update exhausted retries", "taskId", id)
	return false
}

func (ts *taskStore) expireRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ids, err := ts.rdb.SMembers(ctx, redisTaskInflightKey).Result()
	if err != nil {
		slog.Warn("vaultrun-mcp: redis inflight scan failed", "err", err)
		return
	}
	now := time.Now().UTC()
	ageCutoff := now.Add(-ts.maxAge)

	for _, id := range ids {
		if !validTaskID(id) {
			// Defense: drop garbage members that cannot be legitimate tasks.
			_ = ts.rdb.SRem(ctx, redisTaskInflightKey, id).Err()
			continue
		}
		rec, ok := ts.redisGet(id)
		if !ok {
			_ = ts.rdb.SRem(ctx, redisTaskInflightKey, id).Err()
			continue
		}
		if rec.terminal() {
			_ = ts.rdb.SRem(ctx, redisTaskInflightKey, id).Err()
			continue
		}
		if rec.CreatedAt.Before(ageCutoff) {
			ts.update(id, func(t *taskRecord) bool {
				if t.terminal() {
					return false
				}
				t.Status = taskFailed
				t.Error = "task exceeded max age"
				t.Message = "timed out"
				t.Progress = 1
				t.FinishedAt = now
				return true
			})
			if fn := ts.takeCancel(id); fn != nil {
				fn()
			}
			ts.publishCancel(id)
		}
	}
	// Terminal TTL is handled by Redis EXPIRE on the task key; no active delete needed.
}

func (ts *taskStore) publishCancel(id string) {
	if !ts.redisEnabled() || !validTaskID(id) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ts.rdb.Publish(ctx, redisTaskCancelChan, id).Err(); err != nil {
		slog.Debug("vaultrun-mcp: cancel publish failed", "taskId", id, "err", err)
	}
}

func (ts *taskStore) listenCancelPubSub() {
	if ts.rdb == nil {
		return
	}
	sub := ts.rdb.Subscribe(context.Background(), redisTaskCancelChan)
	ch := sub.Channel()
	for msg := range ch {
		id := msg.Payload
		if !validTaskID(id) {
			continue
		}
		if fn := ts.takeCancel(id); fn != nil {
			fn()
		}
	}
}
