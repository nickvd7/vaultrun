package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

type Logger struct {
	db      *sqlx.DB
	hmacKey []byte
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
	entry.Sig = l.sign(entry)

	if err := dbpkg.CreateAuditLog(ctx, l.db, entry); err != nil {
		slog.Error("audit log write failed", "action", e.Action, "err", err)
	}
}

// sign computes HMAC-SHA256 over the immutable fields of an audit log entry.
// Returns "" when no HMAC key is configured.
// Covered fields: id, timestamp (RFC3339Nano), actor, action, session_id,
// run_id, metadata JSON. Fields are separated by null bytes to prevent
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
	h.Write(canonicalMetadata(entry.Metadata))
	return hex.EncodeToString(h.Sum(nil))
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
	return hmac.Equal([]byte(entry.Sig), []byte(l.sign(entry)))
}
