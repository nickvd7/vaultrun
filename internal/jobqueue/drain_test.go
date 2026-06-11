package jobqueue

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRunner counts ExecutePrepared calls and sleeps for a configured duration.
// It satisfies the Queue.work() requirement (access to runner.Runner is via
// the unexported memQueue, so we test Drain indirectly through Submit + Drain).

func TestMemQueueDrainWaitsForInFlight(t *testing.T) {
	// Build a queue with 1 worker and a buffer of 4.
	// We use a memQueue directly (white-box) to avoid needing a full runner.
	executed := atomic.Int32{}
	ch := make(chan Job, 4)
	q := &memQueue{
		ch:         ch,
		httpClient: nil,
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		for range q.ch {
			// Simulate slow job.
			time.Sleep(20 * time.Millisecond)
			executed.Add(1)
		}
	}()

	// Submit 3 jobs directly to the channel (bypassing nil-runner check).
	for i := 0; i < 3; i++ {
		q.ch <- Job{}
	}

	// Drain with a generous timeout — all 3 should complete.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q.Drain(ctx)

	if got := executed.Load(); got != 3 {
		t.Errorf("expected 3 jobs executed after Drain, got %d", got)
	}
}

func TestMemQueueDrainTimesOut(t *testing.T) {
	ch := make(chan Job, 4)
	q := &memQueue{
		ch:         ch,
		httpClient: nil,
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		for range q.ch {
			// Simulate very slow job that won't finish in time.
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Submit one job.
	q.ch <- Job{}

	// Drain with a very short timeout — should time out.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	q.Drain(ctx) // should return after ~10ms even though job hasn't finished
	// If we reach here without deadlock, the test passes.
}

func TestMemQueueSubmitAfterDrainReturnsFalse(t *testing.T) {
	ch := make(chan Job, 4)
	q := &memQueue{
		ch:         ch,
		httpClient: nil,
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		for range q.ch {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	q.Drain(ctx)

	// Submit after Drain should not panic and should return false.
	ok := q.Submit(Job{})
	if ok {
		t.Error("expected Submit after Drain to return false, got true")
	}
}
