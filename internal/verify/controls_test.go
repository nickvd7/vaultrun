package verify

import (
	"testing"
)

func TestRunControlsDiscriminates(t *testing.T) {
	rep := RunControls()
	if rep.SuiteID != SuiteID {
		t.Fatalf("suite_id %q", rep.SuiteID)
	}
	if rep.Fingerprint == "" || len(rep.Fingerprint) != 64 {
		t.Fatalf("bad fingerprint %q", rep.Fingerprint)
	}
	if !rep.Passed || !rep.PipelineDiscriminates {
		t.Fatalf("suite should pass: %+v", rep)
	}
	if len(rep.Controls) < 4 {
		t.Fatalf("expected >=4 controls, got %d", len(rep.Controls))
	}
	var pos, neg int
	for _, c := range rep.Controls {
		if !c.Consistent {
			t.Fatalf("control %s inconsistent: expected=%v observed=%v", c.ID, c.ExpectedPassed, c.ObservedPassed)
		}
		switch c.Kind {
		case ControlPositive:
			pos++
			if !c.ObservedPassed {
				t.Fatalf("positive %s must pass", c.ID)
			}
		case ControlNegative:
			neg++
			if c.ObservedPassed {
				t.Fatalf("negative %s must fail", c.ID)
			}
		}
	}
	if pos < 1 || neg < 1 {
		t.Fatalf("need both kinds: pos=%d neg=%d", pos, neg)
	}
}

func TestSuiteFingerprintStable(t *testing.T) {
	a := SuiteFingerprint()
	b := SuiteFingerprint()
	if a != b {
		t.Fatalf("fingerprint unstable: %s vs %s", a, b)
	}
	// Pin so accidental suite edits bump SuiteID + this digest in review.
	const want = "ec040bb6db99f56c2b68e8163a097108880367bad0ea642f7dd153edddd1be3a"
	if a != want {
		t.Fatalf("fingerprint changed (bump SuiteID if intentional):\n got  %s\n want %s", a, want)
	}
}

func TestControlCasesPublic(t *testing.T) {
	cases := ControlCasesPublic()
	if len(cases) != len(builtInControls()) {
		t.Fatal("mismatch")
	}
}
