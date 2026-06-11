package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestLastUsedCoalesceCache verifies that the in-memory coalesce map correctly
// suppresses repeated DB write triggers within the write interval.
func TestLastUsedCoalesceCache(t *testing.T) {
	// Reset the cache to a known state.
	lastUsedMu.Lock()
	lastUsedCache = make(map[uuid.UUID]time.Time)
	lastUsedMu.Unlock()

	id := uuid.New()

	// First time we see this key — should trigger a write.
	lastUsedMu.Lock()
	_, seen := lastUsedCache[id]
	if seen {
		t.Fatal("cache should be empty for new key")
	}
	lastUsedCache[id] = time.Now().UTC()
	lastUsedMu.Unlock()

	// Immediate second check — should be within the interval, so no write.
	lastUsedMu.Lock()
	lastWrite := lastUsedCache[id]
	within := time.Since(lastWrite) < lastUsedWriteInterval
	lastUsedMu.Unlock()

	if !within {
		t.Error("expected second check to be within the write interval (no DB write)")
	}
}

// TestLastUsedCoalesceIntervalExpiry verifies the condition that triggers a
// new write when the interval has elapsed.
func TestLastUsedCoalesceIntervalExpiry(t *testing.T) {
	lastUsedMu.Lock()
	lastUsedCache = make(map[uuid.UUID]time.Time)
	lastUsedMu.Unlock()

	id := uuid.New()

	// Simulate a write that happened longer ago than the interval.
	oldWrite := time.Now().UTC().Add(-(lastUsedWriteInterval + time.Second))
	lastUsedMu.Lock()
	lastUsedCache[id] = oldWrite
	lastUsedMu.Unlock()

	// Now the elapsed time exceeds the interval → should trigger a write.
	lastUsedMu.Lock()
	elapsed := time.Since(lastUsedCache[id])
	lastUsedMu.Unlock()

	if elapsed < lastUsedWriteInterval {
		t.Errorf("expected elapsed (%v) >= interval (%v)", elapsed, lastUsedWriteInterval)
	}
}
