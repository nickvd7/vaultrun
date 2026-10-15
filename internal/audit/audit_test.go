package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/models"
)

func roundTrip(t *testing.T, e *models.AuditLog) *models.AuditLog {
	t.Helper()
	// What Postgres does: timestamptz keeps microseconds, jsonb comes back
	// parsed with numbers as float64.
	out := *e
	out.Timestamp = e.Timestamp.Truncate(time.Microsecond)
	raw, err := json.Marshal(e.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	out.Metadata = nil
	if err := json.Unmarshal(raw, &out.Metadata); err != nil {
		t.Fatal(err)
	}
	return &out
}

func newEntry() *models.AuditLog {
	sid := uuid.New()
	return &models.AuditLog{
		ID:        uuid.New(),
		Timestamp: time.Now().UTC().Truncate(time.Microsecond),
		Actor:     "actor",
		SessionID: &sid,
		Action:    "command.finished",
		Metadata:  models.JSONB{"status": "completed", "duration_ms": int64(48), "nested": map[string]any{"b": 1, "a": []int{1, 2}}},
	}
}

func TestSignatureSurvivesStorageRoundTrip(t *testing.T) {
	l := New(nil, "k")
	e := newEntry()
	e.Sig = l.sign(e)
	if !l.VerifyAuditSig(roundTrip(t, e)) {
		t.Fatal("signature must verify after a microsecond/JSONB round trip")
	}
}

func TestTamperedEntryFailsVerification(t *testing.T) {
	l := New(nil, "k")
	e := newEntry()
	e.Sig = l.sign(e)
	got := roundTrip(t, e)
	got.Action = "command.failed"
	if l.VerifyAuditSig(got) {
		t.Fatal("changed action must fail verification")
	}
	got = roundTrip(t, e)
	got.Metadata["status"] = "failed"
	if l.VerifyAuditSig(got) {
		t.Fatal("changed metadata must fail verification")
	}
}

func TestWrongKeyAndUnsigned(t *testing.T) {
	e := newEntry()
	e.Sig = New(nil, "k").sign(e)
	if New(nil, "other").VerifyAuditSig(roundTrip(t, e)) {
		t.Fatal("different key must not verify")
	}
	off := New(nil, "")
	if off.Enabled() || off.VerifyAuditSig(e) {
		t.Fatal("signing disabled: nothing verifies")
	}
}
