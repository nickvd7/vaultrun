package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	preds, err := dbpkg.AuditPredsBySeq(ctx, l.db, want)
	if err != nil {
		slog.Error("audit: reading previous entries failed", "err", err)
		preds = map[int64]dbpkg.AuditPred{}
	}

	for _, e := range entries {
		e.SigStatus, e.SigReason = l.classify(e, head, vs, preds)
		valid := e.SigStatus == models.AuditSigValid
		e.SigValid = &valid
		if e.SigStatus == models.AuditSigInvalid {
			e.SigKind = reasonKind(e.SigReason)
			var seq int64
			if e.Seq != nil {
				seq = *e.Seq
			}
			e.SigDetail = reasonDetail(e.SigReason, seq)
		}
		if e.Seq != nil && *e.Seq > 1 {
			if pred, ok := preds[*e.Seq-1]; ok && e.Timestamp.Before(pred.Timestamp) {
				e.OutOfOrder = true
				e.SigDetail = strings.TrimSpace(e.SigDetail + " " + outOfOrderDetail(*e.Seq-1))
			}
		}
	}
}

// reasonKind says whether a reason means the entry itself was altered or only
// that the entry before it was.
func reasonKind(reason string) string {
	switch reason {
	case ReasonPrevChanged, ReasonPrevMissing:
		return models.AuditKindChain
	case "":
		return ""
	}
	return models.AuditKindAltered
}

// reasonDetail explains a reason in full sentences. seq is the entry's own
// chain number (0 when it has none).
func reasonDetail(reason string, seq int64) string {
	prev := "the entry before it"
	if seq > 1 {
		prev = fmt.Sprintf("the entry before it (#%d)", seq-1)
	}
	switch reason {
	case ReasonChanged:
		return "The content or signature of this entry does not match what was signed when it was written, so it was altered afterwards (its time, actor, action or details)."
	case ReasonSigRemoved:
		return "This entry has no signature, although every entry written since signing began has one. The signature was removed."
	case ReasonSeqRemoved:
		return "The chain number of this entry was removed, so it no longer has a place in the chain."
	case ReasonUnchained:
		return "This entry has no chain number although it was written after chaining began, so it was inserted or rewritten outside the API."
	case ReasonFirstHasParent:
		return "The first entry in the chain claims a predecessor, which cannot exist."
	case ReasonPrevChanged:
		return "This entry itself is intact. " + capitalize(prev) + " was changed after this entry was written: the signature this entry recorded for it no longer matches. This warning is a consequence of that change, not of tampering with this entry."
	case ReasonPrevMissing:
		return "This entry itself is intact. " + capitalize(prev) + " was deleted, which breaks the chain at this point."
	}
	return ""
}

func outOfOrderDetail(prevSeq int64) string {
	return fmt.Sprintf("It is dated earlier than the entry before it (#%d), which an untouched log never shows, so one of the two timestamps was changed.", prevSeq)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (l *Logger) classify(e *models.AuditLog, head chainHead, vs *time.Time, preds map[int64]dbpkg.AuditPred) (status, reason string) {
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
		if pred.Sig != e.PrevSig {
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

// IntegrityEntry names one entry that failed verification.
type IntegrityEntry struct {
	ID         uuid.UUID `json:"id"`
	Seq        *int64    `json:"seq,omitempty"`
	Kind       string    `json:"kind"`
	Reason     string    `json:"reason"`
	Detail     string    `json:"detail"`
	OutOfOrder bool      `json:"out_of_order,omitempty"`
	actor      string
}

// Integrity scopes.
const (
	ScopeAll = "all"
	ScopeOwn = "own"
)

// Integrity is the verdict over the whole log, not one page of it.
type Integrity struct {
	Status         string    `json:"status"`
	Scope          string    `json:"scope"`
	CheckedAt      time.Time `json:"checked_at"`
	ChainedEntries int64     `json:"chained_entries"`
	LegacyEntries  int64     `json:"legacy_entries"`
	// InvalidEntries = AlteredEntries + AffectedEntries. Altered entries were
	// themselves changed; affected ones are intact but follow a changed entry.
	InvalidEntries  int64    `json:"invalid_entries"`
	AlteredEntries  int64    `json:"altered_entries"`
	AffectedEntries int64    `json:"affected_entries"`
	MissingEntries  int64    `json:"missing_entries"`
	Problems        []string `json:"problems"`
	// For a key that cannot see the whole log: how many of the invalid entries
	// were recorded under its own name and how many belong to other keys.
	OwnInvalidEntries   *int64           `json:"own_invalid_entries,omitempty"`
	OtherInvalidEntries *int64           `json:"other_invalid_entries,omitempty"`
	Entries             []IntegrityEntry `json:"entries,omitempty"`
	InvalidIDs          []uuid.UUID      `json:"invalid_ids,omitempty"`
	MissingSeqs         []string         `json:"missing_seqs,omitempty"`

	all          []IntegrityEntry
	headProblems []string
}

// ScopedTo returns the verdict as seen by one key: the same status, but only
// the key's own entries are listed; other keys' entries are replaced by a count.
func (i *Integrity) ScopedTo(actor string) *Integrity {
	out := *i
	out.Scope = ScopeOwn
	out.Entries, out.InvalidIDs, out.MissingSeqs = nil, nil, nil
	var own, other int64
	for _, e := range i.all {
		if e.actor == actor {
			own++
			if len(out.Entries) < maxReportedIDs {
				out.Entries = append(out.Entries, e)
				out.InvalidIDs = append(out.InvalidIDs, e.ID)
			}
		} else {
			other++
		}
	}
	// Entries past the cap on the stored list are all attributed to others.
	other += i.InvalidEntries - int64(len(i.all))
	out.OwnInvalidEntries, out.OtherInvalidEntries = &own, &other

	out.Problems = append([]string{}, i.headProblems...)
	if own > 0 {
		out.Problems = append(out.Problems, entryCount(own, "recorded under your key failed verification (marked Invalid in the table)", "recorded under your key failed verification (marked Invalid in the table)"))
	}
	if other > 0 {
		out.Problems = append(out.Problems, entryCount(other, "recorded under another key failed verification (not visible to you)", "recorded under other keys failed verification (not visible to you)"))
	}
	if i.MissingEntries > 0 {
		out.Problems = append(out.Problems, entryCount(i.MissingEntries, "is missing from the chain (deleted entries cannot be attributed to a key)", "are missing from the chain (deleted entries cannot be attributed to a key)"))
	}
	return &out
}

const (
	maxReportedIDs   = 25
	maxTrackedBad    = 10000
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
	res := &Integrity{Scope: ScopeAll, CheckedAt: time.Now().UTC(), Problems: []string{}}
	addInvalid := func(e *models.AuditLog, reason string, outOfOrder bool) {
		res.InvalidEntries++
		kind := reasonKind(reason)
		if kind == models.AuditKindChain {
			res.AffectedEntries++
		} else {
			res.AlteredEntries++
		}
		var seq int64
		if e.Seq != nil {
			seq = *e.Seq
		}
		ie := IntegrityEntry{ID: e.ID, Seq: e.Seq, Kind: kind, Reason: reason, Detail: reasonDetail(reason, seq), OutOfOrder: outOfOrder, actor: e.Actor}
		if outOfOrder {
			ie.Detail = strings.TrimSpace(ie.Detail + " " + outOfOrderDetail(seq-1))
		}
		if len(res.all) < maxTrackedBad {
			res.all = append(res.all, ie)
		}
		if len(res.Entries) < maxReportedIDs {
			res.Entries = append(res.Entries, ie)
			res.InvalidIDs = append(res.InvalidIDs, e.ID)
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
		prevTime   time.Time
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
		gap := (first && expectNext > 0 && seq > expectNext) || (!first && !contiguous)
		if gap {
			from := expectNext
			if !first {
				from = prevSeq + 1
			}
			res.MissingEntries += seq - from
			res.MissingSeqs = appendRange(res.MissingSeqs, from, seq-1)
		}
		reason := ""
		switch {
		case e.Sig == "":
			reason = ReasonSigRemoved
		case !l.VerifyAuditSig(&e):
			reason = ReasonChanged
		case seq == 1 && e.PrevSig != "":
			reason = ReasonFirstHasParent
		case contiguous && e.PrevSig != prevSig:
			reason = ReasonPrevChanged
		case gap:
			reason = ReasonPrevMissing
		}
		if reason != "" {
			addInvalid(&e, reason, contiguous && e.Timestamp.Before(prevTime))
		}
		first, prevSeq, prevSig, prevTime = false, seq, e.Sig, e.Timestamp
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
			switch {
			case e.Sig == "":
				addInvalid(&e, ReasonSigRemoved, false)
			case !l.VerifyAuditSig(&e):
				if strings.HasPrefix(e.Sig, ChainSigPrefix) {
					addInvalid(&e, ReasonSeqRemoved, false)
				} else {
					addInvalid(&e, ReasonChanged, false)
				}
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

	res.headProblems = append([]string{}, res.Problems...)
	if res.AlteredEntries > 0 {
		res.Problems = append(res.Problems, entryCount(res.AlteredEntries, "was altered (its signature does not match or was removed)", "were altered (their signature does not match or was removed)"))
	}
	if res.AffectedEntries > 0 {
		res.Problems = append(res.Problems, entryCount(res.AffectedEntries, "is intact but follows a changed entry", "are intact but follow a changed entry"))
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
