package verify

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBuildEvidenceCheckpointAndControls(t *testing.T) {
	want := true
	zero := 0
	in := EvidenceInput{
		Actor:           "tester",
		StepName:        "install",
		HasCheckpoint:   true,
		IncludeControls: true,
		Spec:            Spec{ExitCodeZero: &want, StdoutContains: "Successfully"},
		Observation:     Observation{ExitCode: &zero, Stdout: "Successfully done"},
		Result:          Evaluate(Spec{ExitCodeZero: &want, StdoutContains: "Successfully"}, Observation{ExitCode: &zero, Stdout: "Successfully done"}, nil),
		HMACKey:         []byte("test-hmac-key-32bytes-long!!!!!!"),
	}
	sid := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	in.SessionID = &sid

	rec, err := BuildEvidence(in)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Schema != EvidenceSchema || rec.Kind != EvidenceKindCombined {
		t.Fatalf("schema/kind: %+v", rec)
	}
	if !rec.Passed || rec.ContentDigest == "" || rec.Sig == "" {
		t.Fatalf("incomplete seal: %+v", rec)
	}
	if rec.Subject.SessionID != sid.String() || rec.Subject.Actor != "tester" {
		t.Fatalf("subject: %+v", rec.Subject)
	}
	if err := VerifyEvidenceIntegrity(rec, in.HMACKey); err != nil {
		t.Fatal(err)
	}
}

func TestBuildEvidenceControlsOnly(t *testing.T) {
	rec, err := BuildEvidence(EvidenceInput{IncludeControls: true})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != EvidenceKindControls || rec.Checkpoint != nil || rec.Controls == nil {
		t.Fatalf("%+v", rec)
	}
	if !rec.Controls.PipelineDiscriminates {
		t.Fatal("controls should discriminate")
	}
}

func TestBuildEvidenceRequiresSource(t *testing.T) {
	if _, err := BuildEvidence(EvidenceInput{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestEvidenceDigestDetectsTamper(t *testing.T) {
	rec, err := BuildEvidence(EvidenceInput{IncludeControls: true, HMACKey: []byte("k")})
	if err != nil {
		t.Fatal(err)
	}
	rec.Passed = !rec.Passed
	if err := VerifyEvidenceIntegrity(rec, []byte("k")); err == nil {
		t.Fatal("expected digest failure after tamper")
	}
}

func TestEvidenceSigDetectsWrongKey(t *testing.T) {
	rec, err := BuildEvidence(EvidenceInput{IncludeControls: true, HMACKey: []byte("correct-key")})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvidenceIntegrity(rec, []byte("wrong-key")); err == nil {
		t.Fatal("expected sig failure")
	}
}

func TestEvidenceTruncatesHugeStdout(t *testing.T) {
	want := true
	zero := 0
	huge := strings.Repeat("A", MaxEvidenceStdoutBytes+1000)
	rec, err := BuildEvidence(EvidenceInput{
		HasCheckpoint: true,
		Spec:          Spec{ExitCodeZero: &want},
		Observation:   Observation{ExitCode: &zero, Stdout: huge},
		Result:        Result{Passed: true, Checks: []Check{{Name: "exit_code_zero", Passed: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Checkpoint.Observation.Stdout) > MaxEvidenceStdoutBytes+4 {
		t.Fatalf("stdout not truncated: %d", len(rec.Checkpoint.Observation.Stdout))
	}
}

func TestEvidenceStripsNullBytes(t *testing.T) {
	want := true
	zero := 0
	rec, err := BuildEvidence(EvidenceInput{
		HasCheckpoint: true,
		Spec:          Spec{ExitCodeZero: &want},
		Observation:   Observation{ExitCode: &zero, Stdout: "ok\x00secret"},
		Result:        Result{Passed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Checkpoint.Observation.Stdout, "\x00") {
		t.Fatal("nul not stripped")
	}
}

func TestEvidenceRejectsLongStepName(t *testing.T) {
	_, err := BuildEvidence(EvidenceInput{
		IncludeControls: true,
		StepName:        strings.Repeat("x", 201),
	})
	if err == nil {
		t.Fatal("expected step_name error")
	}
}

func TestVerifyEvidenceSchema(t *testing.T) {
	rec, _ := BuildEvidence(EvidenceInput{IncludeControls: true})
	rec.Schema = "other"
	// Recompute won't help — Verify checks schema first
	if err := VerifyEvidenceIntegrity(rec, nil); err == nil {
		t.Fatal("expected schema error")
	}
}

func TestEvidenceDigestStableAcrossRecompute(t *testing.T) {
	rec, err := BuildEvidence(EvidenceInput{IncludeControls: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ComputeContentDigest(rec)
	if err != nil {
		t.Fatal(err)
	}
	if got != rec.ContentDigest {
		t.Fatalf("digest drift: %s vs %s", got, rec.ContentDigest)
	}
}

func TestEvidenceSigMissingWhenKeyRequired(t *testing.T) {
	rec, err := BuildEvidence(EvidenceInput{IncludeControls: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyEvidenceIntegrity(rec, []byte("key")); err == nil {
		t.Fatal("expected sig missing error")
	}
}

func TestEvidenceRejectsLongActor(t *testing.T) {
	_, err := BuildEvidence(EvidenceInput{
		IncludeControls: true,
		Actor:           strings.Repeat("a", 257),
	})
	if err == nil {
		t.Fatal("expected actor error")
	}
}

func TestEvidenceNilIntegrity(t *testing.T) {
	if err := VerifyEvidenceIntegrity(nil, nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ComputeContentDigest(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestSignEvidenceEmptyKey(t *testing.T) {
	if SignEvidence("abc", nil) != "" {
		t.Fatal("empty key must yield empty sig")
	}
}

func TestEvidenceCheckpointOnlyKind(t *testing.T) {
	want := true
	zero := 0
	rec, err := BuildEvidence(EvidenceInput{
		HasCheckpoint: true,
		Spec:          Spec{ExitCodeZero: &want},
		Observation:   Observation{ExitCode: &zero},
		Result:        Result{Passed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != EvidenceKindCheckpoint || rec.Controls != nil {
		t.Fatalf("%+v", rec)
	}
}

func TestEvidenceCombinedFailedWhenEitherFails(t *testing.T) {
	want := true
	one := 1
	rec, err := BuildEvidence(EvidenceInput{
		HasCheckpoint:   true,
		IncludeControls: true,
		Spec:            Spec{ExitCodeZero: &want},
		Observation:     Observation{ExitCode: &one},
		Result:          Result{Passed: false, Checks: []Check{{Name: "exit_code_zero", Passed: false}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Passed {
		t.Fatal("combined must fail when checkpoint fails")
	}
	if rec.Kind != EvidenceKindCombined {
		t.Fatalf("kind=%s", rec.Kind)
	}
}
