//go:build integration

package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nickvd7/vaultrun/internal/audit"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
)

func chainLogger(t *testing.T, n int) *audit.Logger {
	t.Helper()
	truncateFeatureTables(t)
	al := audit.New(testDB, testHMACKey)
	for i := 1; i <= n; i++ {
		al.Log(context.Background(), audit.Event{Actor: "alice", Action: fmt.Sprintf("test.event%d", i)})
	}
	return al
}

func chainEntries(t *testing.T, al *audit.Logger) map[string]*models.AuditLog {
	t.Helper()
	logs, err := dbpkg.ListAuditLogsFiltered(context.Background(), testDB, dbpkg.AuditFilter{}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	al.Classify(context.Background(), logs)
	out := map[string]*models.AuditLog{}
	for _, l := range logs {
		out[l.Action] = l
	}
	return out
}

func integrity(t *testing.T, al *audit.Logger) *audit.Integrity {
	t.Helper()
	res, err := al.Integrity(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAuditChainIntact(t *testing.T) {
	al := chainLogger(t, 5)
	res := integrity(t, al)
	if res.Status != audit.IntegrityOK || res.ChainedEntries != 5 || len(res.Problems) != 0 {
		t.Fatalf("intact chain: %+v", res)
	}
	for action, e := range chainEntries(t, al) {
		if e.SigStatus != models.AuditSigValid || e.Seq == nil {
			t.Fatalf("%s: status=%q seq=%v", action, e.SigStatus, e.Seq)
		}
	}
}

func TestAuditChainConcurrentWritersLeaveNoGaps(t *testing.T) {
	al := chainLogger(t, 0)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			al.Log(context.Background(), audit.Event{Actor: "alice", Action: fmt.Sprintf("test.concurrent%d", i)})
		}(i)
	}
	wg.Wait()
	res := integrity(t, al)
	if res.Status != audit.IntegrityOK || res.ChainedEntries != 30 {
		t.Fatalf("concurrent writes: %+v", res)
	}
}

// The three tampering styles from the v0.4.4 test round: edit the entry, edit
// it and clear its signature, and edit it, clear the signature and move the
// timestamp back before signing started.
func TestAuditChainDetectsEditedEntries(t *testing.T) {
	cases := map[string]string{
		"changed action":                `UPDATE audit_logs SET action = 'test.forged' WHERE action = 'test.event3'`,
		"changed and signature removed": `UPDATE audit_logs SET action = 'test.forged', sig = '' WHERE action = 'test.event3'`,
		"signature removed, chain number removed, time moved back": `UPDATE audit_logs SET action = 'test.forged', sig = '', seq = NULL,
			timestamp = timestamp - interval '30 days' WHERE action = 'test.event3'`,
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			al := chainLogger(t, 5)
			if _, err := testDB.Exec(stmt); err != nil {
				t.Fatal(err)
			}
			res := integrity(t, al)
			if res.Status != audit.IntegrityCompromised {
				t.Fatalf("tampering not detected: %+v", res)
			}
			if res.InvalidEntries+res.MissingEntries == 0 {
				t.Fatalf("no entry reported: %+v", res)
			}
			// The entry after the edited one also shows the break when the edit
			// removed its chain number.
			if strings.Contains(name, "chain number") {
				if res.MissingEntries != 1 || len(res.MissingSeqs) != 1 || res.MissingSeqs[0] != "3" {
					t.Fatalf("hole at sequence 3 not reported: %+v", res)
				}
				if e := chainEntries(t, al)["test.event4"]; e.SigStatus != models.AuditSigInvalid || e.SigReason != audit.ReasonPrevMissing {
					t.Fatalf("successor: status=%q reason=%q", e.SigStatus, e.SigReason)
				}
			} else if e := chainEntries(t, al)["test.forged"]; e.SigStatus != models.AuditSigInvalid {
				t.Fatalf("edited entry: status=%q", e.SigStatus)
			}
		})
	}
}

func TestAuditChainDetectsDeletedEntries(t *testing.T) {
	t.Run("middle", func(t *testing.T) {
		al := chainLogger(t, 5)
		testDB.MustExec(`DELETE FROM audit_logs WHERE action = 'test.event3'`)
		res := integrity(t, al)
		if res.Status != audit.IntegrityCompromised || res.MissingEntries != 1 || res.MissingSeqs[0] != "3" {
			t.Fatalf("deleted middle entry: %+v", res)
		}
		if e := chainEntries(t, al)["test.event4"]; e.SigStatus != models.AuditSigInvalid {
			t.Fatalf("successor of deleted entry: %q", e.SigStatus)
		}
		if res.AffectedEntries != 1 || len(res.Entries) != 1 || res.Entries[0].Reason != audit.ReasonPrevMissing || *res.Entries[0].Seq != 4 {
			t.Fatalf("the entry after the hole should be listed as affected: %+v", res.Entries)
		}
	})
	t.Run("newest", func(t *testing.T) {
		al := chainLogger(t, 5)
		testDB.MustExec(`DELETE FROM audit_logs WHERE action IN ('test.event4', 'test.event5')`)
		res := integrity(t, al)
		if res.Status != audit.IntegrityCompromised || res.MissingEntries != 2 || res.MissingSeqs[0] != "4-5" {
			t.Fatalf("deleted newest entries: %+v", res)
		}
	})
	t.Run("everything", func(t *testing.T) {
		al := chainLogger(t, 3)
		testDB.MustExec(`DELETE FROM audit_logs`)
		if res := integrity(t, al); res.Status != audit.IntegrityCompromised || res.MissingEntries != 3 {
			t.Fatalf("deleted all entries: %+v", res)
		}
	})
}

func TestAuditChainDetectsHeadTampering(t *testing.T) {
	al := chainLogger(t, 4)
	testDB.MustExec(`UPDATE audit_chain SET pruned_through = 4`)
	if res := integrity(t, al); res.Status != audit.IntegrityCompromised {
		t.Fatalf("altered head: %+v", res)
	}

	al = chainLogger(t, 4)
	testDB.MustExec(`DELETE FROM audit_chain`)
	if res := integrity(t, al); res.Status != audit.IntegrityCompromised {
		t.Fatalf("missing head: %+v", res)
	}
	// Logging carries on, appending to the chain without repairing the head.
	al.Log(context.Background(), audit.Event{Actor: "alice", Action: "test.after"})
	if res := integrity(t, al); res.Status != audit.IntegrityCompromised || res.ChainedEntries != 5 {
		t.Fatalf("head still missing after more writes: %+v", res)
	}
}

// chainLoggerAged writes oldN correctly signed entries dated 100 days ago and
// then newN entries dated now, so retention has something legitimate to delete.
func chainLoggerAged(t *testing.T, oldN, newN int) *audit.Logger {
	t.Helper()
	truncateFeatureTables(t)
	al := audit.New(testDB, testHMACKey)
	old := time.Now().AddDate(0, 0, -100)
	al.SetClock(func() time.Time { old = old.Add(time.Second); return old })
	for i := 1; i <= oldN; i++ {
		al.Log(context.Background(), audit.Event{Actor: "alice", Action: fmt.Sprintf("test.event%d", i)})
	}
	al.SetClock(time.Now)
	for i := oldN + 1; i <= oldN+newN; i++ {
		al.Log(context.Background(), audit.Event{Actor: "alice", Action: fmt.Sprintf("test.event%d", i)})
	}
	return al
}

func auditCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := testDB.Get(&n, `SELECT COUNT(*) FROM audit_logs`); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAuditRetentionPrunesChainWithoutBreakingIt(t *testing.T) {
	al := chainLoggerAged(t, 2, 3)
	n, err := al.Prune(context.Background(), time.Now().AddDate(0, 0, -30))
	if err != nil || n != 2 {
		t.Fatalf("prune: n=%d err=%v", n, err)
	}
	res := integrity(t, al)
	if res.Status != audit.IntegrityOK || res.ChainedEntries != 3 {
		t.Fatalf("after retention: %+v", res)
	}
	for action, e := range chainEntries(t, al) {
		if e.SigStatus != models.AuditSigValid {
			t.Fatalf("%s after retention: %q (%s)", action, e.SigStatus, e.SigReason)
		}
	}
	// Deleting an entry beyond the pruned prefix is still caught.
	testDB.MustExec(`DELETE FROM audit_logs WHERE action = 'test.event4'`)
	if res := integrity(t, al); res.Status != audit.IntegrityCompromised {
		t.Fatalf("hole after retention: %+v", res)
	}
}

// A recent entry whose timestamp was moved back must not make retention delete
// the newer entries before it, nor turn the verdict back to ok.
func TestAuditRetentionIgnoresBackdatedEntry(t *testing.T) {
	cutoff := time.Now().AddDate(0, 0, -30)

	t.Run("only recent entries", func(t *testing.T) {
		al := chainLogger(t, 6)
		testDB.MustExec(`UPDATE audit_logs SET timestamp = '2026-01-01' WHERE action = 'test.event6'`)
		n, err := al.Prune(context.Background(), cutoff)
		if !errors.Is(err, audit.ErrPruneBlocked) || n != 0 || auditCount(t) != 6 {
			t.Fatalf("prune: n=%d err=%v left=%d", n, err, auditCount(t))
		}
		if res := integrity(t, al); res.Status != audit.IntegrityCompromised || res.MissingEntries != 0 {
			t.Fatalf("verdict: %+v", res)
		}
	})

	t.Run("backdated entry behind recent ones pauses retention, nothing is deleted", func(t *testing.T) {
		al := chainLoggerAged(t, 2, 4)
		testDB.MustExec(`UPDATE audit_logs SET timestamp = '2026-01-01' WHERE action = 'test.event6'`)
		n, err := al.Prune(context.Background(), cutoff)
		if !errors.Is(err, audit.ErrPruneBlocked) || !strings.Contains(err.Error(), "#6") || n != 0 || auditCount(t) != 6 {
			t.Fatalf("prune: n=%d err=%v left=%d", n, err, auditCount(t))
		}
		res := integrity(t, al)
		if res.Status != audit.IntegrityCompromised || res.AlteredEntries != 1 || res.MissingEntries != 0 {
			t.Fatalf("the backdated entry must still be reported: %+v", res)
		}
	})

	t.Run("backdated entry among the old ones blocks retention", func(t *testing.T) {
		al := chainLoggerAged(t, 4, 2)
		testDB.MustExec(`UPDATE audit_logs SET timestamp = timestamp - interval '1 day' WHERE action = 'test.event3'`)
		n, err := al.Prune(context.Background(), cutoff)
		if !errors.Is(err, audit.ErrPruneBlocked) || n != 0 || auditCount(t) != 6 {
			t.Fatalf("prune: n=%d err=%v left=%d", n, err, auditCount(t))
		}
		if res := integrity(t, al); res.Status != audit.IntegrityCompromised {
			t.Fatalf("verdict: %+v", res)
		}
	})
}

func TestAuditRetentionRefusesToPruneAnUnverifiedPrefix(t *testing.T) {
	cutoff := time.Now().AddDate(0, 0, -30)
	cases := map[string]string{
		"altered entry":       `UPDATE audit_logs SET action = 'test.forged' WHERE action = 'test.event2'`,
		"signature removed":   `UPDATE audit_logs SET sig = '' WHERE action = 'test.event2'`,
		"deleted entry":       `DELETE FROM audit_logs WHERE action = 'test.event2'`,
		"deleted first entry": `DELETE FROM audit_logs WHERE action = 'test.event1'`,
		"altered chain head":  `UPDATE audit_chain SET pruned_through = 0, last_sig = 'x'`,
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			al := chainLoggerAged(t, 3, 2)
			testDB.MustExec(stmt)
			before := auditCount(t)
			n, err := al.Prune(context.Background(), cutoff)
			if !errors.Is(err, audit.ErrPruneBlocked) || n != 0 || auditCount(t) != before {
				t.Fatalf("prune: n=%d err=%v left=%d of %d", n, err, auditCount(t), before)
			}
		})
	}
}

func TestAuditRetentionKeepsUnnumberedEntriesFromAfterChainingBegan(t *testing.T) {
	al := chainLoggerAged(t, 2, 1)
	testDB.MustExec(`INSERT INTO audit_logs (id, timestamp, actor, action, metadata, sig)
		VALUES (gen_random_uuid(), now() - interval '50 days', 'alice', 'test.unnumbered', '{}', '')`)
	testDB.MustExec(`INSERT INTO audit_logs (id, timestamp, actor, action, metadata, sig)
		VALUES (gen_random_uuid(), now() - interval '200 days', 'alice', 'test.legacy', '{}', '')`)
	if _, err := al.Prune(context.Background(), time.Now().AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	var actions []string
	if err := testDB.Select(&actions, `SELECT action FROM audit_logs WHERE seq IS NULL`); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0] != "test.unnumbered" {
		t.Fatalf("unnumbered entries left: %v", actions)
	}
	if res := integrity(t, al); res.Status != audit.IntegrityCompromised {
		t.Fatalf("the unnumbered entry must still be reported: %+v", res)
	}
}

func TestAuditEntriesBeforeChainingStayLegacy(t *testing.T) {
	truncateFeatureTables(t)
	// An entry from v0.4.1 to v0.4.3: signed (bare digest) or v0.4.0: unsigned,
	// written before chaining began.
	testDB.MustExec(`INSERT INTO audit_logs (id, timestamp, actor, action, metadata, sig)
		VALUES (gen_random_uuid(), now() - interval '10 days', 'alice', 'test.old', '{}', '')`)
	al := audit.New(testDB, testHMACKey)
	al.Log(context.Background(), audit.Event{Actor: "alice", Action: "test.new"})
	res := integrity(t, al)
	if res.Status != audit.IntegrityOK || res.LegacyEntries != 1 || res.ChainedEntries != 1 {
		t.Fatalf("legacy + chained: %+v", res)
	}
	got := chainEntries(t, al)
	if got["test.old"].SigStatus != models.AuditSigUnsigned || got["test.new"].SigStatus != models.AuditSigValid {
		t.Fatalf("old=%q new=%q", got["test.old"].SigStatus, got["test.new"].SigStatus)
	}
}

func TestAuditIntegrityTellsAlteredFromAffectedEntries(t *testing.T) {
	al := chainLogger(t, 8)
	testDB.MustExec(`UPDATE audit_logs SET action = 'test.forged', sig = '' WHERE action = 'test.event3'`)
	testDB.MustExec(`UPDATE audit_logs SET timestamp = timestamp - interval '30 days' WHERE action = 'test.event6'`)
	res := integrity(t, al)
	if res.Status != audit.IntegrityCompromised || res.AlteredEntries != 2 || res.AffectedEntries != 1 || res.InvalidEntries != 3 {
		t.Fatalf("want 2 altered + 1 affected: %+v", res)
	}
	byseq := map[int64]audit.IntegrityEntry{}
	for _, e := range res.Entries {
		byseq[*e.Seq] = e
	}
	if e := byseq[3]; e.Kind != models.AuditKindAltered || e.Reason != audit.ReasonSigRemoved || e.Detail == "" {
		t.Fatalf("entry 3: %+v", e)
	}
	if e := byseq[4]; e.Kind != models.AuditKindChain || e.Reason != audit.ReasonPrevChanged || !strings.Contains(e.Detail, "(#3)") {
		t.Fatalf("entry 4: %+v", e)
	}
	if e := byseq[6]; e.Kind != models.AuditKindAltered || !e.OutOfOrder || !strings.Contains(e.Detail, "(#5)") {
		t.Fatalf("entry 6 (backdated): %+v", e)
	}

	entries := chainEntries(t, al)
	if e := entries["test.event6"]; !e.OutOfOrder || e.SigKind != models.AuditKindAltered || e.SigDetail == "" {
		t.Fatalf("backdated entry on the page: %+v", e)
	}
	if e := entries["test.event4"]; e.SigKind != models.AuditKindChain || e.OutOfOrder {
		t.Fatalf("affected entry on the page: %+v", e)
	}
	if e := entries["test.event5"]; e.SigStatus != models.AuditSigValid || e.OutOfOrder || e.SigKind != "" {
		t.Fatalf("untouched entry: %+v", e)
	}

	// Chain order puts the backdated entry between its neighbours.
	logs, err := dbpkg.ListAuditLogsFiltered(context.Background(), testDB, dbpkg.AuditFilter{BySeq: true}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range logs {
		if *l.Seq != int64(len(logs)-i) {
			t.Fatalf("chain order broken at %d: seq %d", i, *l.Seq)
		}
	}
}

func TestAuditIntegrityScopedToOneKey(t *testing.T) {
	al := chainLogger(t, 4)
	al.Log(context.Background(), audit.Event{Actor: "bob", Action: "test.bob1"})
	testDB.MustExec(`UPDATE audit_logs SET action = 'test.forged', sig = '' WHERE action = 'test.event2'`)
	testDB.MustExec(`UPDATE audit_logs SET action = 'test.forged', sig = '' WHERE action = 'test.bob1'`)
	res := integrity(t, al)
	if res.InvalidEntries != 3 {
		t.Fatalf("want 3 invalid (2 altered + 1 affected): %+v", res)
	}
	alice := res.ScopedTo("alice")
	if alice.Scope != audit.ScopeOwn || *alice.OwnInvalidEntries != 2 || *alice.OtherInvalidEntries != 1 {
		t.Fatalf("alice: own=%d other=%d", *alice.OwnInvalidEntries, *alice.OtherInvalidEntries)
	}
	if len(alice.Entries) != 2 || len(alice.InvalidIDs) != 2 || len(alice.MissingSeqs) != 0 {
		t.Fatalf("alice should be told about her own two entries only: %+v", alice)
	}
	for _, e := range alice.Entries {
		if *e.Seq == 5 {
			t.Fatalf("scoped verdict leaks bob's entry: %+v", e)
		}
	}
	if alice.Status != audit.IntegrityCompromised || len(alice.Problems) == 0 {
		t.Fatalf("scoped verdict lost the status: %+v", alice)
	}
	if alice.ChainedEntries != 4 || res.ChainedEntries != 5 {
		t.Fatalf("chained_entries must count the key's own entries: alice=%d total=%d", alice.ChainedEntries, res.ChainedEntries)
	}
	for _, p := range alice.Problems {
		if strings.Contains(p, "Invalid") {
			t.Fatalf("scoped problems must use Altered/Affected wording: %q", p)
		}
	}
	bob := res.ScopedTo("bob")
	if bob.ChainedEntries != 1 {
		t.Fatalf("bob chained_entries = %d", bob.ChainedEntries)
	}
	if *bob.OwnInvalidEntries != 1 || *bob.OtherInvalidEntries != 2 {
		t.Fatalf("bob: own=%d other=%d", *bob.OwnInvalidEntries, *bob.OtherInvalidEntries)
	}
}
