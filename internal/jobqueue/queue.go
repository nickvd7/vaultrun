// Package jobqueue provides worker pool implementations for asynchronous run
// execution. Jobs are submitted by the async run endpoint and executed by a
// fixed pool of goroutines. On completion an optional webhook callback is fired.
//
// Two implementations are available:
//   - MemQueue   — in-process bounded channel (default; jobs lost on restart)
//   - RedisQueue — Redis Streams backed (durable; jobs survive restarts)
//
// Use New() for the in-memory variant and NewRedis() for the Redis variant.
// Both satisfy the Queue interface consumed by the rest of the codebase.
//
// Webhook security: when a WEBHOOK_SECRET is configured every callback POST
// carries an X-VaultRun-Signature header of the form "sha256=<hex>", computed
// as HMAC-SHA256 of the raw request body. Receivers should verify this before
// processing the payload.
package jobqueue

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/runner"
)

// Queue is the interface satisfied by both MemQueue and RedisQueue.
type Queue interface {
	// Submit enqueues a job. Returns false (job dropped) when at capacity.
	Submit(j Job) bool
	// Len returns the approximate number of pending jobs.
	Len() int
}

// Job is one unit of async work.
type Job struct {
	// Req is the run request to execute.
	Req runner.RunRequest
	// Run is the pre-created pending run record (created by runner.PrepareAsync).
	Run *models.Run
	// CallbackURL, when non-empty, receives an HTTP POST with the run result.
	CallbackURL string
}

// New creates an in-memory Queue with workers concurrent goroutines and a
// buffer of bufSize pending jobs. webhookSecret is used to HMAC-sign callback
// payloads (pass "" to disable signing).
func New(rnr *runner.Runner, workers, bufSize int, webhookSecret string) Queue {
	if workers <= 0 {
		workers = 4
	}
	if bufSize <= 0 {
		bufSize = 256
	}
	q := &memQueue{
		ch:            make(chan Job, bufSize),
		runner:        rnr,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		webhookSecret: webhookSecret,
	}
	for i := 0; i < workers; i++ {
		go q.work()
	}
	return q
}

// memQueue is the in-process, bounded channel implementation of Queue.
type memQueue struct {
	ch            chan Job
	runner        *runner.Runner
	httpClient    *http.Client
	webhookSecret string
}

// Submit enqueues a job. Returns false (job dropped) when the buffer is full.
// The caller should return HTTP 503 in that case.
func (q *memQueue) Submit(j Job) bool {
	select {
	case q.ch <- j:
		return true
	default:
		slog.Warn("jobqueue: queue full, dropping job",
			"session_id", j.Run.SessionID, "run_id", j.Run.ID)
		return false
	}
}

// Len returns the number of jobs currently waiting in the buffer.
func (q *memQueue) Len() int { return len(q.ch) }

func (q *memQueue) work() {
	for j := range q.ch {
		run, err := q.runner.ExecutePrepared(context.Background(), j.Req, j.Run)
		if j.CallbackURL != "" {
			sendCallback(q.httpClient, q.webhookSecret, j.CallbackURL, run, err)
		}
	}
}

// sendCallback fires a one-shot HTTP POST to the configured callback URL.
// When a webhookSecret is set the request includes an X-VaultRun-Signature
// header: "sha256=<hmac-hex>" computed over the raw JSON body.
// Failures are logged but do not affect the run result.
func sendCallback(httpClient *http.Client, webhookSecret, callbackURL string, run *models.Run, execErr error) {
	type payload struct {
		RunID    string      `json:"run_id,omitempty"`
		Status   string      `json:"status"`
		ExitCode *int        `json:"exit_code,omitempty"`
		Error    string      `json:"error,omitempty"`
		Run      *models.Run `json:"run,omitempty"`
	}

	p := payload{Run: run}
	if run != nil {
		p.RunID = run.ID.String()
		p.Status = run.Status
		p.ExitCode = run.ExitCode
	}
	if execErr != nil {
		p.Error = execErr.Error()
		if p.Status == "" {
			p.Status = models.RunStatusFailed
		}
	}

	b, err := json.Marshal(p)
	if err != nil {
		slog.Warn("jobqueue: marshal callback payload", "err", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, callbackURL, bytes.NewReader(b))
	if err != nil {
		slog.Warn("jobqueue: build callback request", "url", callbackURL, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// HMAC-SHA256 signature so receivers can verify authenticity.
	if webhookSecret != "" {
		mac := hmac.New(sha256.New, []byte(webhookSecret))
		mac.Write(b)
		req.Header.Set("X-VaultRun-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		slog.Warn("jobqueue: callback POST failed", "url", callbackURL, "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Warn("jobqueue: callback returned error status",
			"url", callbackURL, "status", resp.StatusCode)
	}
}
