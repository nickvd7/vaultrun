package verify

import (
	"errors"
	"testing"
)

func TestEvaluateExitCodeZero(t *testing.T) {
	want := true
	zero := 0
	fail := 1

	r := Evaluate(Spec{ExitCodeZero: &want}, Observation{ExitCode: &zero}, nil)
	if !r.Passed {
		t.Fatalf("want pass for exit 0: %+v", r)
	}
	r = Evaluate(Spec{ExitCodeZero: &want}, Observation{ExitCode: &fail}, nil)
	if r.Passed {
		t.Fatalf("want fail for exit 1: %+v", r)
	}
	r = Evaluate(Spec{ExitCodeZero: &want}, Observation{}, nil)
	if r.Passed {
		t.Fatalf("want fail when exit missing: %+v", r)
	}
}

func TestEvaluateStdoutContains(t *testing.T) {
	r := Evaluate(Spec{StdoutContains: "ok"}, Observation{Stdout: "all ok here"}, nil)
	if !r.Passed {
		t.Fatalf("want pass: %+v", r)
	}
	r = Evaluate(Spec{StdoutContains: "missing"}, Observation{Stdout: "all ok"}, nil)
	if r.Passed {
		t.Fatalf("want fail: %+v", r)
	}
}

func TestEvaluateFileExists(t *testing.T) {
	probe := func(path string) (bool, error) {
		if path == "/out.txt" {
			return true, nil
		}
		if path == "/boom" {
			return false, errors.New("stat failed")
		}
		return false, nil
	}
	r := Evaluate(Spec{FileExists: "/out.txt"}, Observation{}, probe)
	if !r.Passed {
		t.Fatalf("want pass: %+v", r)
	}
	r = Evaluate(Spec{FileExists: "/nope"}, Observation{}, probe)
	if r.Passed {
		t.Fatalf("want fail: %+v", r)
	}
	r = Evaluate(Spec{FileExists: "/out.txt"}, Observation{}, nil)
	if r.Passed {
		t.Fatalf("want fail without probe: %+v", r)
	}
	r = Evaluate(Spec{FileExists: "/boom"}, Observation{}, probe)
	if r.Passed {
		t.Fatalf("want fail on probe error: %+v", r)
	}
}

func TestEvaluateCombined(t *testing.T) {
	want := true
	zero := 0
	r := Evaluate(Spec{
		ExitCodeZero:   &want,
		StdoutContains: "Successfully",
		FileExists:     "/done",
	}, Observation{ExitCode: &zero, Stdout: "Successfully installed"}, func(string) (bool, error) {
		return true, nil
	})
	if !r.Passed || len(r.Checks) != 3 {
		t.Fatalf("want 3 passing checks: %+v", r)
	}
}

func TestSpecEmpty(t *testing.T) {
	if !(Spec{}).Empty() {
		t.Fatal("empty spec should be Empty")
	}
	s := Spec{StdoutContains: "x"}
	if s.Empty() {
		t.Fatal("non-empty")
	}
}
