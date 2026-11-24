package audit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
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

func TestSigStatusSeparatesLegacyFromTampered(t *testing.T) {
	l := New(nil, "k")
	since := time.Now().UTC().Add(-time.Hour)

	signed := newEntry()
	signed.Sig = l.sign(signed)
	if got := l.SigStatus(roundTrip(t, signed), &since); got != models.AuditSigValid {
		t.Fatalf("intact entry = %q, want valid", got)
	}

	altered := roundTrip(t, signed)
	altered.Action = "command.failed"
	if got := l.SigStatus(altered, &since); got != models.AuditSigInvalid {
		t.Fatalf("altered entry = %q, want invalid", got)
	}

	stripped := roundTrip(t, signed)
	stripped.Sig = ""
	if got := l.SigStatus(stripped, &since); got != models.AuditSigInvalid {
		t.Fatalf("entry with its signature removed = %q, want invalid", got)
	}

	old := newEntry()
	old.Timestamp = since.Add(-time.Hour)
	if got := l.SigStatus(old, &since); got != models.AuditSigUnsigned {
		t.Fatalf("unsigned entry from before signing = %q, want unsigned", got)
	}
	old.Sig = "deadbeef"
	if got := l.SigStatus(old, &since); got != models.AuditSigUnverifiable {
		t.Fatalf("old-scheme signature from before signing = %q, want unverifiable", got)
	}

	if got := l.SigStatus(newEntry(), nil); got != models.AuditSigUnsigned {
		t.Fatalf("nothing verifies yet, no signature = %q, want unsigned", got)
	}
	bad := newEntry()
	bad.Sig = "deadbeef"
	if got := l.SigStatus(bad, nil); got != models.AuditSigInvalid {
		t.Fatalf("nothing verifies yet, bad signature = %q, want invalid", got)
	}
}

func TestBareHexSignaturesFromV041StillVerify(t *testing.T) {
	l := New(nil, "k")
	e := newEntry()
	e.Sig = strings.TrimPrefix(l.sign(e), SigPrefix)
	if !l.VerifyAuditSig(roundTrip(t, e)) {
		t.Fatal("a v0.4.1-v0.4.3 entry (bare hex digest) must keep verifying")
	}
	if !strings.HasPrefix(l.sign(e), SigPrefix) {
		t.Fatal("new signatures carry the marker")
	}
}

func TestChainedSignatureCoversSequenceAndPredecessor(t *testing.T) {
	l := New(nil, "k")
	e := newEntry()
	seq := int64(7)
	e.Seq, e.PrevSig = &seq, "v3:abc"
	e.Sig = l.sign(e)
	if !strings.HasPrefix(e.Sig, ChainSigPrefix) {
		t.Fatalf("chained signature prefix: %q", e.Sig)
	}
	if !l.VerifyAuditSig(roundTrip(t, e)) {
		t.Fatal("chained signature must verify after a round trip")
	}
	for name, mutate := range map[string]func(*models.AuditLog){
		"sequence number": func(x *models.AuditLog) { n := int64(8); x.Seq = &n },
		"predecessor":     func(x *models.AuditLog) { x.PrevSig = "v3:def" },
		"chain removed":   func(x *models.AuditLog) { x.Seq, x.PrevSig = nil, "" },
	} {
		got := roundTrip(t, e)
		mutate(got)
		if l.VerifyAuditSig(got) {
			t.Fatalf("changing the %s must fail verification", name)
		}
	}
}

func TestPreviousKeyVerifiesOldEntryAndHead(t *testing.T) {
	old := New(nil, "old-key")
	e := newEntry()
	e.Sig = old.sign(e)

	cur := New(nil, "new-key")
	cur.SetPreviousKeys([]string{" old-key ", "new-key", ""})
	if !cur.VerifyAuditSig(roundTrip(t, e)) {
		t.Fatal("an entry signed with a previous key must still verify")
	}
	fresh := newEntry()
	fresh.Sig = cur.sign(fresh)
	if old.VerifyAuditSig(roundTrip(t, fresh)) {
		t.Fatal("a new signature must not verify with only the previous key")
	}
	if !cur.VerifyAuditSig(roundTrip(t, fresh)) {
		t.Fatal("a new signature must verify with the current key")
	}

	started := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	head := &dbpkg.AuditChainHead{Seq: 4, LastSig: e.Sig, PrunedThrough: 0, StartedAt: started}
	head.MAC = old.headMAC(head)
	if !cur.headValid(head) {
		t.Fatal("chain head signed with a previous key must still verify")
	}
	head.MAC = cur.headMAC(head)
	if !cur.headValid(head) {
		t.Fatal("chain head signed with the current key must verify")
	}
	head.MAC = "not-a-mac"
	if cur.headValid(head) {
		t.Fatal("a head that matches neither key must not verify")
	}
}
