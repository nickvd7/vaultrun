//go:build integration

package handlers_test

import (
	"context"
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

func TestAuditRetentionPrunesChainWithoutBreakingIt(t *testing.T) {
	al := chainLogger(t, 5)
	testDB.MustExec(`UPDATE audit_logs SET timestamp = timestamp - interval '100 days' WHERE action IN ('test.event1', 'test.event2')`)
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
