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
	const want = "3ff5bff0af6edf0f3bc38a813193a81799a1ba419f9d031766c619d79c5ec1d1"
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

func TestControlSuiteV2AntiShortcutIDs(t *testing.T) {
	want := map[string]bool{
		"reference-repair":           true,
		"no-repair":                  false,
		"stdout-miss":                false,
		"nonzero-required":           true,
		"file-missing-despite-green": false,
		"stderr-only-success":        false,
		"exit-nonzero-with-file":     false,
		"wrong-filename":             false,
	}
	rep := RunControls()
	if len(rep.Controls) != len(want) {
		t.Fatalf("got %d controls, want %d", len(rep.Controls), len(want))
	}
	seen := map[string]bool{}
	for _, c := range rep.Controls {
		exp, ok := want[c.ID]
		if !ok {
			t.Fatalf("unexpected control %s", c.ID)
		}
		if c.ExpectedPassed != exp || c.ObservedPassed != exp || !c.Consistent {
			t.Fatalf("%s: expected=%v observed=%v consistent=%v", c.ID, c.ExpectedPassed, c.ObservedPassed, c.Consistent)
		}
		seen[c.ID] = true
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("missing control %s", id)
		}
	}
	if rep.SuiteID != "vaultrun-verify-controls-v2" {
		t.Fatalf("suite_id %q", rep.SuiteID)
	}
}

func TestAntiShortcutStderrNotCountedAsStdout(t *testing.T) {
	// Direct evaluator check: success token only on stderr must fail stdout_contains.
	want := true
	zero := 0
	r := Evaluate(
		Spec{ExitCodeZero: &want, StdoutContains: "Successfully"},
		Observation{ExitCode: &zero, Stdout: "", Stderr: "Successfully installed"},
		nil,
	)
	if r.Passed {
		t.Fatal("stderr marker must not satisfy stdout_contains")
	}
}

func TestAntiShortcutWrongFilename(t *testing.T) {
	want := true
	zero := 0
	probe := func(path string) (bool, error) {
		return path == "out/done.txt.bak", nil
	}
	r := Evaluate(
		Spec{ExitCodeZero: &want, StdoutContains: "Successfully", FileExists: "out/done.txt"},
		Observation{ExitCode: &zero, Stdout: "Successfully installed package"},
		probe,
	)
	if r.Passed {
		t.Fatal("wrong filename must fail")
	}
}
