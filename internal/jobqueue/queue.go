// Package jobqueue provides an in-process worker pool for asynchronous run
// execution. Jobs are submitted by the async run endpoint and executed by a
// fixed pool of goroutines. On completion an optional webhook callback is fired.
//
// Limitations: the queue is in-memory. Pending jobs are lost if the process
// restarts. For durable queuing consider an external broker (Redis Streams,
// PostgreSQL LISTEN/NOTIFY, etc.).
package jobqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/runner"
)

// Job is one unit of async work.
type Job struct {
	// Req is the run request to execute.
	Req runner.RunRequest
	// Run is the pre-created pending run record (created by runner.PrepareAsync).
	Run *models.Run
	// CallbackURL, when non-empty, receives an HTTP POST with the run result.
	CallbackURL string
}

// Queue is a bounded in-process worker pool.
type Queue struct {
	ch         chan Job
	runner     *runner.Runner
	httpClient *http.Client
}

// New creates a Queue with workers concurrent goroutines and a buffer of
// bufSize pending jobs. Sensible defaults: workers=4, bufSize=256.
func New(rnr *runner.Runner, workers, bufSize int) *Queue {
	if workers <= 0 {
		workers = 4
	}
	if bufSize <= 0 {
		bufSize = 256
	}
	q := &Queue{
		ch:         make(chan Job, bufSize),
		runner:     rnr,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for i := 0; i < workers; i++ {
		go q.work()
	}
	return q
}

// Submit enqueues a job. Returns false (job dropped) when the buffer is full.
// The caller should return HTTP 503 in that case.
func (q *Queue) Submit(j Job) bool {
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
func (q *Queue) Len() int { return len(q.ch) }

func (q *Queue) work() {
	for j := range q.ch {
		run, err := q.runner.ExecutePrepared(context.Background(), j.Req, j.Run)
		if j.CallbackURL != "" {
			q.sendCallback(j.CallbackURL, run, err)
		}
	}
}

// sendCallback fires a one-shot HTTP POST to the configured callback URL.
// Failures are logged but do not affect the run result.
func (q *Queue) sendCallback(callbackURL string, run *models.Run, execErr error) {
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

	resp, err := q.httpClient.Post(callbackURL, "application/json", bytes.NewReader(b))
	if err != nil {
		slog.Warn("jobqueue: callback POST failed",
			"url", callbackURL, "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Warn("jobqueue: callback returned error status",
			"url", callbackURL, "status", resp.StatusCode)
	}
}
