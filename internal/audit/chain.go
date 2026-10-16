package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

// Reasons reported next to an invalid entry.
const (
	ReasonChanged        = "entry or signature was changed"
	ReasonSigRemoved     = "signature was removed"
	ReasonSeqRemoved     = "chain number was removed"
	ReasonUnchained      = "entry without chain number written after chaining began"
	ReasonPrevMissing    = "previous entry was deleted"
	ReasonPrevChanged    = "previous entry was changed"
	ReasonFirstHasParent = "first entry claims a predecessor"
)

// chainHead is the stored head plus whether it is present and untampered.
type chainHead struct {
	head   *dbpkg.AuditChainHead
	exists bool
	valid  bool
}

func (l *Logger) loadHead(ctx context.Context) (chainHead, error) {
	h, err := dbpkg.GetAuditChainHead(ctx, l.db, false)
	if errors.Is(err, sql.ErrNoRows) {
		return chainHead{}, nil
	}
	if err != nil {
		return chainHead{}, err
	}
	return chainHead{head: h, exists: true, valid: l.headValid(h)}, nil
}

// Classify sets SigStatus, SigValid and SigReason on every entry of a page
// read from the database. It needs AUDIT_HMAC_KEY; without it entries are
// left untouched.
func (l *Logger) Classify(ctx context.Context, entries []*models.AuditLog) {
	if !l.Enabled() || len(entries) == 0 {
		return
	}
	head, err := l.loadHead(ctx)
	if err != nil {
		slog.Error("audit: reading chain head failed", "err", err)
	}
	vs := l.VerifiedSince(ctx)

	var want []int64
	for _, e := range entries {
		if e.Seq != nil && *e.Seq > 1 {
			want = append(want, *e.Seq-1)
		}
	}
	preds, err := dbpkg.AuditSigsBySeq(ctx, l.db, want)
	if err != nil {
		slog.Error("audit: reading previous entries failed", "err", err)
		preds = map[int64]string{}
	}

	for _, e := range entries {
		e.SigStatus, e.SigReason = l.classify(e, head, vs, preds)
		valid := e.SigStatus == models.AuditSigValid
		e.SigValid = &valid
	}
}

func (l *Logger) classify(e *models.AuditLog, head chainHead, vs *time.Time, preds map[int64]string) (status, reason string) {
	if e.Seq == nil {
		if e.Sig != "" && l.VerifyAuditSig(e) {
			return models.AuditSigValid, ""
		}
		if len(e.Sig) >= len(ChainSigPrefix) && e.Sig[:len(ChainSigPrefix)] == ChainSigPrefix {
			return models.AuditSigInvalid, ReasonSeqRemoved
		}
		// Only entries from before chaining can lack a number. A tampered
		// chained entry could be dressed up as one, but not with a timestamp
		// before the (signed) start of the chain without leaving a hole in it.
		if head.valid && !e.Timestamp.Before(head.head.StartedAt) {
			if e.Sig == "" {
				return models.AuditSigInvalid, ReasonSigRemoved
			}
			return models.AuditSigInvalid, ReasonUnchained
		}
		s := l.SigStatus(e, vs)
		if s == models.AuditSigInvalid {
			return s, ReasonChanged
		}
		return s, ""
	}

	if e.Sig == "" {
		return models.AuditSigInvalid, ReasonSigRemoved
	}
	if !l.VerifyAuditSig(e) {
		return models.AuditSigInvalid, ReasonChanged
	}
	seq := *e.Seq
	if seq == 1 {
		if e.PrevSig != "" {
			return models.AuditSigInvalid, ReasonFirstHasParent
		}
		return models.AuditSigValid, ""
	}
	if pred, ok := preds[seq-1]; ok {
		if pred != e.PrevSig {
			return models.AuditSigInvalid, ReasonPrevChanged
		}
		return models.AuditSigValid, ""
	}
	var pruned int64
	if head.valid {
		pruned = head.head.PrunedThrough
	}
	if seq-1 > pruned {
		return models.AuditSigInvalid, ReasonPrevMissing
	}
	return models.AuditSigValid, ""
}

// Integrity statuses.
const (
	IntegrityOK          = "ok"
	IntegrityCompromised = "compromised"
	IntegrityNotStarted  = "not_started"
	IntegrityDisabled    = "disabled"
)

// Integrity is the verdict over the whole log, not one page of it.
type Integrity struct {
	Status         string      `json:"status"`
	CheckedAt      time.Time   `json:"checked_at"`
	ChainedEntries int64       `json:"chained_entries"`
	LegacyEntries  int64       `json:"legacy_entries"`
	InvalidEntries int64       `json:"invalid_entries"`
	MissingEntries int64       `json:"missing_entries"`
	Problems       []string    `json:"problems"`
	InvalidIDs     []uuid.UUID `json:"invalid_ids,omitempty"`
	MissingSeqs    []string    `json:"missing_seqs,omitempty"`
}

const (
	maxReportedIDs   = 25
	integrityCacheTT = 10 * time.Second
)

// Integrity verifies the whole log: every signature, every link of the chain,
// holes in the numbering, entries cut off the end and the chain head itself.
// The result is cached briefly so a page refresh does not rescan the table.
func (l *Logger) Integrity(ctx context.Context, force bool) (*Integrity, error) {
	if !l.Enabled() {
		return &Integrity{Status: IntegrityDisabled, CheckedAt: time.Now().UTC(), Problems: []string{}}, nil
	}
	l.scanMu.Lock()
	defer l.scanMu.Unlock()
	if !force && l.integrity != nil && time.Since(l.integrityAt) < integrityCacheTT {
		return l.integrity, nil
	}
	res, err := l.scan(ctx)
	if err != nil {
		return nil, err
	}
	l.integrity, l.integrityAt = res, time.Now()
	return res, nil
}

func (l *Logger) scan(ctx context.Context) (*Integrity, error) {
	head, err := l.loadHead(ctx)
	if err != nil {
		return nil, err
	}
	res := &Integrity{CheckedAt: time.Now().UTC(), Problems: []string{}}
	addInvalid := func(id uuid.UUID) {
		res.InvalidEntries++
		if len(res.InvalidIDs) < maxReportedIDs {
			res.InvalidIDs = append(res.InvalidIDs, id)
		}
	}

	rows, err := l.db.QueryxContext(ctx, `SELECT * FROM audit_logs WHERE seq IS NOT NULL ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	var (
		first      = true
		prevSeq    int64
		prevSig    string
		lastSeq    int64
		lastSig    string
		haveChain  bool
		expectNext int64
	)
	if head.valid {
		expectNext = head.head.PrunedThrough + 1
	}
	for rows.Next() {
		var e models.AuditLog
		if err := rows.StructScan(&e); err != nil {
			_ = rows.Close()
			return nil, err
		}
		haveChain = true
		res.ChainedEntries++
		seq := *e.Seq
		contiguous := !first && seq == prevSeq+1
		if (first && expectNext > 0 && seq > expectNext) || (!first && !contiguous) {
			from := expectNext
			if !first {
				from = prevSeq + 1
			}
			res.MissingEntries += seq - from
			res.MissingSeqs = appendRange(res.MissingSeqs, from, seq-1)
		}
		bad := e.Sig == "" || !l.VerifyAuditSig(&e)
		if !bad {
			switch {
			case seq == 1 && e.PrevSig != "":
				bad = true
			case contiguous && e.PrevSig != prevSig:
				bad = true
			}
		}
		if bad {
			addInvalid(e.ID)
		}
		first, prevSeq, prevSig = false, seq, e.Sig
		lastSeq, lastSig = seq, e.Sig
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	switch {
	case !head.exists && haveChain:
		res.Problems = append(res.Problems, "the chain head is missing")
	case head.exists && !head.valid:
		res.Problems = append(res.Problems, "the chain head was altered or AUDIT_HMAC_KEY changed")
	case head.valid:
		switch {
		case !haveChain && head.head.Seq > head.head.PrunedThrough:
			n := head.head.Seq - head.head.PrunedThrough
			res.MissingEntries += n
			res.MissingSeqs = appendRange(res.MissingSeqs, head.head.PrunedThrough+1, head.head.Seq)
		case haveChain && lastSeq < head.head.Seq:
			res.MissingEntries += head.head.Seq - lastSeq
			res.MissingSeqs = appendRange(res.MissingSeqs, lastSeq+1, head.head.Seq)
		case haveChain && lastSeq > head.head.Seq:
			res.Problems = append(res.Problems, "the chain head is behind the newest entry")
		case haveChain && lastSig != head.head.LastSig:
			res.Problems = append(res.Problems, "the newest entry does not match the chain head")
		}
	}

	if head.valid {
		urows, err := l.db.QueryxContext(ctx, `SELECT * FROM audit_logs WHERE seq IS NULL AND timestamp >= $1`, head.head.StartedAt)
		if err != nil {
			return nil, err
		}
		for urows.Next() {
			var e models.AuditLog
			if err := urows.StructScan(&e); err != nil {
				_ = urows.Close()
				return nil, err
			}
			if e.Sig == "" || !l.VerifyAuditSig(&e) {
				addInvalid(e.ID)
			}
		}
		if err := urows.Err(); err != nil {
			_ = urows.Close()
			return nil, err
		}
		_ = urows.Close()
	}
	var n int64
	if err := l.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM audit_logs WHERE seq IS NULL`); err != nil {
		return nil, err
	}
	res.LegacyEntries = n

	if res.InvalidEntries > 0 {
		res.Problems = append(res.Problems, fmt.Sprintf("%s a signature that does not match, or lack one", entryCount(res.InvalidEntries, "has", "have")))
	}
	if res.MissingEntries > 0 {
		res.Problems = append(res.Problems, fmt.Sprintf("%s missing from the chain", entryCount(res.MissingEntries, "is", "are")))
	}
	switch {
	case len(res.Problems) > 0:
		res.Status = IntegrityCompromised
	case !head.exists && !haveChain:
		res.Status = IntegrityNotStarted
	default:
		res.Status = IntegrityOK
	}
	return res, nil
}

func entryCount(n int64, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 entry %s", one)
	}
	return fmt.Sprintf("%d entries %s", n, many)
}

func appendRange(rs []string, from, to int64) []string {
	if len(rs) >= maxReportedIDs {
		return rs
	}
	if from == to {
		return append(rs, fmt.Sprintf("%d", from))
	}
	return append(rs, fmt.Sprintf("%d-%d", from, to))
}

// WatchIntegrity re-verifies the whole log every interval and logs an error
// while it is compromised, so tampering is noticed without anyone opening the
// dashboard. It runs until ctx is cancelled.
func (l *Logger) WatchIntegrity(ctx context.Context, interval time.Duration) {
	if !l.Enabled() {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	check := func() {
		res, err := l.Integrity(ctx, true)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("audit: integrity check failed", "err", err)
			}
			return
		}
		if res.Status == IntegrityCompromised {
			slog.Error("audit: LOG INTEGRITY COMPROMISED",
				"problems", res.Problems, "invalid_entries", res.InvalidEntries,
				"missing_entries", res.MissingEntries, "invalid_ids", res.InvalidIDs, "missing_seqs", res.MissingSeqs)
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

// Prune deletes entries older than before (retention). Chained entries go as a
// prefix of the chain, and the chain head records how far, so a pruned log
// still verifies while a hole elsewhere does not.
func (l *Logger) Prune(ctx context.Context, before time.Time) (int64, error) {
	if !l.Enabled() {
		return dbpkg.DeleteOldAuditLogs(ctx, l.db, before)
	}
	tx, err := l.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLockKey); err != nil {
		return 0, err
	}
	head, err := dbpkg.GetAuditChainHead(ctx, tx, true)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	deleted, maxSeq, err := dbpkg.PruneAuditLogsBefore(ctx, tx, before)
	if err != nil {
		return 0, err
	}
	if head != nil && l.headValid(head) && maxSeq > head.PrunedThrough {
		head.PrunedThrough = maxSeq
		head.MAC = l.headMAC(head)
		if err := dbpkg.UpdateAuditChainHead(ctx, tx, head); err != nil {
			return 0, err
		}
	}
	return deleted, tx.Commit()
}
