package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

// SigPrefix marks signatures made since entries became verifiable. Before that
// (v0.4.0 and earlier) the digest covered nanoseconds the database does not
// store, so those entries can never verify. The marker lets the API tell "old,
// expected to fail" from "new, so a failure means tampering" without luck:
// roughly one legacy entry in a thousand verifies by chance, which makes the
// first entry that merely verifies a poor boundary. v0.4.1 to v0.4.3 wrote bare
// hex digests with the same algorithm; they still verify (see VerifyAuditSig).
const SigPrefix = "v2:"

// ChainSigPrefix marks signatures of chained entries: those also cover their
// sequence number and the previous entry's signature (see the Logger doc).
const ChainSigPrefix = "v3:"

// chainLockKey is the Postgres advisory lock that serialises chain writers.
const chainLockKey int64 = 0x76617574 // "vaut"

// Logger writes audit entries. With a signing key it also chains them: every
// entry gets the next sequence number and carries the previous entry's
// signature, both under its own HMAC, and a signed head row (audit_chain)
// records the newest number, how far retention has pruned and when chaining
// began. Changing an entry, removing its signature, moving its timestamp,
// deleting an entry in the middle or cutting off the newest entries all become
// detectable, none of which per-entry signatures alone could do. Whoever holds
// the key can still rewrite the whole chain, and a database writer who saved an
// older copy of the head can restore it after truncating the tail; shipping
// the log to a store they cannot write (the SIEM export) covers that.
type Logger struct {
	db      *sqlx.DB
	hmacKey []byte

	mu            sync.Mutex
	verifiedSince *time.Time

	scanMu      sync.Mutex
	integrity   *Integrity
	integrityAt time.Time
}

// New creates an audit Logger. hmacKey is the raw HMAC-SHA256 signing key
// (AUDIT_HMAC_KEY env var). Pass an empty string to disable signing.
func New(db *sqlx.DB, hmacKey string) *Logger {
	return &Logger{db: db, hmacKey: []byte(hmacKey)}
}

type Event struct {
	Actor     string
	SessionID *uuid.UUID
	RunID     *uuid.UUID
	Action    string
	Metadata  models.JSONB
}

func (l *Logger) Log(ctx context.Context, e Event) {
	// Postgres timestamptz keeps microseconds. Sign what will be stored, or the
	// signature can never verify after a round trip.
	entry := &models.AuditLog{
		ID:        uuid.New(),
		Timestamp: time.Now().UTC().Truncate(time.Microsecond),
		Actor:     e.Actor,
		SessionID: e.SessionID,
		RunID:     e.RunID,
		Action:    e.Action,
		Metadata:  e.Metadata,
	}
	if entry.Metadata == nil {
		entry.Metadata = models.JSONB{}
	}

	var err error
	if l.Enabled() {
		err = l.logChained(ctx, entry)
	} else {
		err = dbpkg.CreateAuditLog(ctx, l.db, entry)
	}
	if err != nil {
		slog.Error("audit log write failed", "action", e.Action, "err", err)
	}
}

// logChained appends entry to the chain under a lock on the chain head, so
// sequence numbers are gapless and ordered like the timestamps.
func (l *Logger) logChained(ctx context.Context, entry *models.AuditLog) error {
	tx, err := l.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Serialises writers, including the very first one, when there is no head
	// row yet to lock.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLockKey); err != nil {
		return err
	}
	head, err := dbpkg.GetAuditChainHead(ctx, tx, true)
	if errors.Is(err, sql.ErrNoRows) {
		if _, _, lerr := dbpkg.LastChainedAuditEntry(ctx, tx); errors.Is(lerr, sql.ErrNoRows) {
			// First chained entry ever: start the chain now.
			h := &dbpkg.AuditChainHead{StartedAt: time.Now().UTC().Truncate(time.Microsecond)}
			h.MAC = l.headMAC(h)
			if err := dbpkg.InsertAuditChainHead(ctx, tx, h); err != nil {
				return err
			}
			head, err = dbpkg.GetAuditChainHead(ctx, tx, true)
		} else if lerr != nil {
			return lerr
		} else {
			// Chained entries exist but the head row is gone. Keep appending
			// without recreating it, so the evidence stays.
			head, err = nil, nil
		}
	}
	if err != nil {
		return err
	}

	var seq int64
	var prev string
	headTrusted := head != nil && l.headValid(head)
	if headTrusted {
		seq, prev = head.Seq, head.LastSig
	} else {
		slog.Error("audit: chain head is missing or fails verification; appending without updating it")
		lastSeq, lastSig, lerr := dbpkg.LastChainedAuditEntry(ctx, tx)
		if lerr != nil && !errors.Is(lerr, sql.ErrNoRows) {
			return lerr
		}
		seq, prev = lastSeq, lastSig
	}

	next := seq + 1
	entry.Seq = &next
	entry.PrevSig = prev
	entry.Timestamp = time.Now().UTC().Truncate(time.Microsecond)
	entry.Sig = l.sign(entry)
	if err := dbpkg.CreateAuditLog(ctx, tx, entry); err != nil {
		return err
	}
	if headTrusted {
		head.Seq, head.LastSig = next, entry.Sig
		head.MAC = l.headMAC(head)
		if err := dbpkg.UpdateAuditChainHead(ctx, tx, head); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *Logger) headMAC(h *dbpkg.AuditChainHead) string {
	m := hmac.New(sha256.New, l.hmacKey)
	fmt.Fprintf(m, "audit-chain-head\x00%d\x00%s\x00%d\x00%s",
		h.Seq, h.LastSig, h.PrunedThrough, h.StartedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
	return hex.EncodeToString(m.Sum(nil))
}

func (l *Logger) headValid(h *dbpkg.AuditChainHead) bool {
	return hmac.Equal([]byte(h.MAC), []byte(l.headMAC(h)))
}

// sign computes HMAC-SHA256 over the immutable fields of an audit log entry.
// Returns "" when no HMAC key is configured.
// Covered fields: id, timestamp (RFC3339Nano), actor, action, session_id,
// run_id, (for chained entries) seq and prev_sig, metadata JSON. Fields are separated by null bytes to prevent
// boundary-confusion attacks.
func (l *Logger) sign(entry *models.AuditLog) string {
	if len(l.hmacKey) == 0 {
		return ""
	}
	h := hmac.New(sha256.New, l.hmacKey)
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00",
		entry.ID.String(),
		entry.Timestamp.UTC().Format(time.RFC3339Nano),
		entry.Actor,
		entry.Action,
		uuidStr(entry.SessionID),
		uuidStr(entry.RunID),
	)
	prefix := SigPrefix
	if entry.Seq != nil {
		fmt.Fprintf(h, "%d\x00%s\x00", *entry.Seq, entry.PrevSig)
		prefix = ChainSigPrefix
	}
	h.Write(canonicalMetadata(entry.Metadata))
	return prefix + hex.EncodeToString(h.Sum(nil))
}

// canonicalMetadata renders metadata the way it looks after a JSONB round
// trip (numbers as float64, keys sorted), so the bytes signed at write time
// equal the bytes verified when the row is read back.
func canonicalMetadata(m models.JSONB) []byte {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func uuidStr(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// Enabled reports whether entries are being signed (AUDIT_HMAC_KEY is set).
func (l *Logger) Enabled() bool { return len(l.hmacKey) > 0 }

// VerifyAuditSig re-computes the HMAC for entry and returns true when it
// matches the stored Sig. Returns false when signing is not configured.
func (l *Logger) VerifyAuditSig(entry *models.AuditLog) bool {
	if len(l.hmacKey) == 0 || entry.Sig == "" {
		return false
	}
	return hmac.Equal([]byte(trimSigPrefix(entry.Sig)), []byte(trimSigPrefix(l.sign(entry))))
}

func trimSigPrefix(s string) string {
	return strings.TrimPrefix(strings.TrimPrefix(s, SigPrefix), ChainSigPrefix)
}

// SigStatus classifies an entry read back from the database.
//
// verifiedSince is the time of the earliest entry carrying SigPrefix (see
// VerifiedSince). Entries from before it were written with signing off, or by
// a version whose signatures could not always be checked, so a missing or
// non-matching signature there is "unsigned" or "unverifiable", which is
// expected. From that moment on every entry must verify, so a mismatch, or a
// missing signature, means the entry was altered or its signature removed and
// is reported "invalid". Without the second rule, clearing the sig column would
// turn a tampered entry into an ordinary-looking unsigned one.
//
// This is evidence, not proof: someone who can rewrite rows could also move an
// entry's timestamp back before verifiedSince. Ship the log to a store they
// cannot write (see the SIEM export) when that matters.
func (l *Logger) SigStatus(entry *models.AuditLog, verifiedSince *time.Time) string {
	if entry.Sig != "" && l.VerifyAuditSig(entry) {
		return models.AuditSigValid
	}
	if verifiedSince != nil && entry.Timestamp.Before(*verifiedSince) {
		if entry.Sig == "" {
			return models.AuditSigUnsigned
		}
		return models.AuditSigUnverifiable
	}
	if entry.Sig == "" && verifiedSince == nil {
		return models.AuditSigUnsigned
	}
	return models.AuditSigInvalid
}

// VerifiedSince returns the time of the earliest entry that carries SigPrefix,
// or nil when there is none (yet). Once found it is cached: that entry cannot
// become later.
func (l *Logger) VerifiedSince(ctx context.Context) *time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.verifiedSince != nil {
		return l.verifiedSince
	}
	ts, err := dbpkg.FirstPrefixedAuditTime(ctx, l.db, SigPrefix)
	if err != nil {
		slog.Error("audit: locating first verifiable entry failed", "err", err)
		return nil
	}
	l.verifiedSince = ts
	return ts
}
