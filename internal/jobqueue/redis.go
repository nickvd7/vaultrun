package jobqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/runner"
)

const (
	redisStreamKey    = "vaultrun:jobs"
	redisGroupName    = "vaultrun-workers"
	redisBlockTimeout = 5 * time.Second

	// reaperInterval is how often the reaper scans for stale pending messages.
	reaperInterval = 30 * time.Second
	// reaperMinIdle is how long a message must sit in the PEL (pending-entry list)
	// before the reaper reclaims it. Must exceed the longest expected normal run
	// to avoid double-processing live jobs.
	reaperMinIdle = 2 * time.Minute
)

// RedisQueue is a durable, Redis Streams-backed Queue. Jobs survive process
// restarts because they are stored in Redis until explicitly acknowledged.
//
// Each worker goroutine uses a named consumer ("worker-N") within the consumer
// group. Unacknowledged jobs from crashed workers can be reclaimed via the
// standard Redis XPENDING / XCLAIM mechanism (not implemented here — add a
// reaper goroutine for production use).
type RedisQueue struct {
	client        *redis.Client
	runner        *runner.Runner
	db            *sqlx.DB
	httpClient    *http.Client
	webhookSecret string
	workers       int
}

// jobPayload is the serialisable form stored in Redis Streams.
// It contains all fields needed to reconstruct a RunRequest and fetch the
// associated *models.Run from the database.
type jobPayload struct {
	RunID          string            `json:"run_id"`
	SessionID      string            `json:"session_id"`
	ContainerID    string            `json:"container_id"`
	Command        string            `json:"command"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
	WorkingDir     string            `json:"working_dir"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Actor          string            `json:"actor"`
	CallbackURL    string            `json:"callback_url"`
	WorkspacePath  string            `json:"workspace_path"` // host path for artifact detection
}

// NewRedis creates a Redis Streams-backed Queue.
//
//   - redisAddr     — host:port of the Redis server (e.g. "localhost:6379")
//   - redisPassword — password (empty = no auth)
//   - redisDB       — logical database index (0–15)
//   - workers       — number of concurrent consumer goroutines
//   - webhookSecret — HMAC key for callback signing (empty = no signing)
//
// Returns an error if the Redis connection cannot be established.
func NewRedis(
	rnr *runner.Runner,
	db *sqlx.DB,
	redisAddr, redisPassword string,
	redisDB, workers int,
	webhookSecret string,
) (Queue, error) {
	if workers <= 0 {
		workers = 4
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       redisDB,
	})

	// Verify connectivity.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	// Create the consumer group (idempotent — ignore BUSYGROUP error).
	// We start from "0" so that any messages left over from a previous run are
	// re-delivered to the new consumer group on startup.
	err := rdb.XGroupCreateMkStream(context.Background(), redisStreamKey, redisGroupName, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return nil, fmt.Errorf("create redis consumer group: %w", err)
	}

	q := &RedisQueue{
		client:        rdb,
		runner:        rnr,
		db:            db,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		webhookSecret: webhookSecret,
		workers:       workers,
	}

	// Launch worker goroutines.
	for i := 0; i < workers; i++ {
		go q.work(fmt.Sprintf("worker-%d", i))
	}

	// Launch the PEL reaper: periodically reclaims messages that have been
	// pending for longer than reaperMinIdle (i.e. the owning consumer crashed
	// before acknowledging). Uses XAUTOCLAIM (Redis ≥ 6.2).
	go q.reap("reaper-0")

	slog.Info("jobqueue: redis queue started",
		"addr", redisAddr, "workers", workers, "stream", redisStreamKey)
	return q, nil
}

// Submit serialises the job to JSON and appends it to the Redis Stream.
// Returns false if the XADD fails (e.g. Redis unreachable).
func (q *RedisQueue) Submit(j Job) bool {
	p := jobPayload{
		RunID:          j.Run.ID.String(),
		SessionID:      j.Run.SessionID.String(),
		ContainerID:    j.Req.ContainerID,
		Command:        j.Req.Command,
		Args:           j.Req.Args,
		Env:            j.Req.Env,
		WorkingDir:     j.Req.WorkingDir,
		TimeoutSeconds: j.Req.TimeoutSeconds,
		Actor:          j.Req.Actor,
		CallbackURL:    j.CallbackURL,
		WorkspacePath:  j.Req.WorkspacePath,
	}

	b, err := json.Marshal(p)
	if err != nil {
		slog.Error("jobqueue redis: marshal job", "err", err)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: redisStreamKey,
		Values: map[string]any{"payload": string(b)},
	}).Err(); err != nil {
		slog.Error("jobqueue redis: XADD failed", "err", err)
		return false
	}
	return true
}

// Len returns the total number of messages in the stream (not only pending).
// Used by the health endpoint for monitoring.
func (q *RedisQueue) Len() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := q.client.XLen(ctx, redisStreamKey).Result()
	if err != nil {
		return -1 // signal unavailability
	}
	return int(n)
}

// work is the consumer goroutine. It blocks on XREADGROUP, processes one job
// at a time, and acknowledges completed jobs.
func (q *RedisQueue) work(consumerName string) {
	for {
		msgs, err := q.client.XReadGroup(context.Background(), &redis.XReadGroupArgs{
			Group:    redisGroupName,
			Consumer: consumerName,
			Streams:  []string{redisStreamKey, ">"},
			Count:    1,
			Block:    redisBlockTimeout,
		}).Result()
		if err == redis.Nil || len(msgs) == 0 || (err != nil && len(msgs) == 0) {
			// Timeout or transient error — loop and retry.
			if err != nil && err != redis.Nil {
				slog.Warn("jobqueue redis: XREADGROUP error", "consumer", consumerName, "err", err)
				time.Sleep(time.Second) // back off on persistent errors
			}
			continue
		}

		for _, msg := range msgs[0].Messages {
			q.processMessage(msg)
		}
	}
}

func (q *RedisQueue) processMessage(msg redis.XMessage) {
	raw, ok := msg.Values["payload"].(string)
	if !ok {
		slog.Error("jobqueue redis: message missing payload field", "id", msg.ID)
		q.ack(msg.ID) // ack to avoid redelivery loop
		return
	}

	var p jobPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		slog.Error("jobqueue redis: unmarshal payload", "id", msg.ID, "err", err)
		q.ack(msg.ID)
		return
	}

	runID, err := uuid.Parse(p.RunID)
	if err != nil {
		slog.Error("jobqueue redis: invalid run_id", "id", msg.ID, "run_id", p.RunID)
		q.ack(msg.ID)
		return
	}

	sessionID, err := uuid.Parse(p.SessionID)
	if err != nil {
		slog.Error("jobqueue redis: invalid session_id", "id", msg.ID, "err", err)
		q.ack(msg.ID)
		return
	}

	// Fetch the pending run record from the database.
	ctx := context.Background()
	run, err := dbpkg.GetRun(ctx, q.db, runID)
	if err != nil {
		slog.Error("jobqueue redis: fetch run", "run_id", runID, "err", err)
		q.ack(msg.ID) // don't retry DB errors — run is stuck pending
		return
	}

	req := runner.RunRequest{
		SessionID:      sessionID,
		ContainerID:    p.ContainerID,
		Command:        p.Command,
		Args:           p.Args,
		Env:            p.Env,
		WorkingDir:     p.WorkingDir,
		TimeoutSeconds: p.TimeoutSeconds,
		Actor:          p.Actor,
		CallbackURL:    p.CallbackURL,
		WorkspacePath:  p.WorkspacePath,
	}

	completedRun, execErr := q.runner.ExecutePrepared(ctx, req, run)

	if p.CallbackURL != "" {
		sendCallback(q.httpClient, q.webhookSecret, p.CallbackURL, completedRun, execErr)
	}

	q.ack(msg.ID)
}

func (q *RedisQueue) ack(msgID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := q.client.XAck(ctx, redisStreamKey, redisGroupName, msgID).Err(); err != nil {
		slog.Warn("jobqueue redis: XACK failed", "msg_id", msgID, "err", err)
	}
}

// reap is the PEL (Pending-Entry List) reaper goroutine. It periodically calls
// XAUTOCLAIM to find messages that have been sitting unacknowledged for longer
// than reaperMinIdle — a sign that the consumer that originally received them
// crashed before processing or ACKing the message — and re-delivers them to
// this consumer for processing.
//
// This implements the "reaper goroutine for production use" noted in the Redis
// Streams design: without it, jobs owned by crashed workers stay stuck forever.
func (q *RedisQueue) reap(consumerName string) {
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()

	for range ticker.C {
		cursor := "0-0"
		for {
			msgs, next, err := q.client.XAutoClaim(context.Background(), &redis.XAutoClaimArgs{
				Stream:   redisStreamKey,
				Group:    redisGroupName,
				Consumer: consumerName,
				MinIdle:  reaperMinIdle,
				Start:    cursor,
				Count:    10,
			}).Result()
			if err != nil {
				slog.Warn("jobqueue redis: XAUTOCLAIM error in reaper", "err", err)
				break
			}
			for _, msg := range msgs {
				slog.Info("jobqueue redis: reaper reclaiming stale message", "msg_id", msg.ID)
				q.processMessage(msg)
			}
			// XAUTOCLAIM returns "0-0" when there are no more pending messages.
			if next == "0-0" || next == "" {
				break
			}
			cursor = next
		}
	}
}
