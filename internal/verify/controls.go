package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// SuiteID is the frozen identity of the built-in verify control suite.
const SuiteID = "vaultrun-verify-controls-v1"

// ControlKind is positive (must pass) or negative (must fail).
type ControlKind string

const (
	ControlPositive ControlKind = "positive"
	ControlNegative ControlKind = "negative"
)

// ControlCase is one frozen evaluator control (FaultWright-style).
type ControlCase struct {
	ID              string      `json:"id"`
	Kind            ControlKind `json:"kind"`
	Name            string      `json:"name"`
	Description     string      `json:"description"`
	Spec            Spec        `json:"spec"`
	Observation     Observation `json:"observation"`
	FileExistsMap   map[string]bool `json:"-"` // probe fixture; not part of public digest payload separately
	ExpectedPassed  bool        `json:"expected_passed"`
}

// ControlOutcome is the evaluation of one control against the live evaluator.
type ControlOutcome struct {
	ID             string      `json:"id"`
	Kind           ControlKind `json:"kind"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	ExpectedPassed bool        `json:"expected_passed"`
	ObservedPassed bool        `json:"observed_passed"`
	Consistent     bool        `json:"consistent"`
	Result         Result      `json:"result"`
}

// ControlReport is the auditable record for a control-suite run.
type ControlReport struct {
	SuiteID               string           `json:"suite_id"`
	Fingerprint           string           `json:"fingerprint"`
	CreatedAt             time.Time        `json:"created_at"`
	Passed                bool             `json:"passed"`
	PipelineDiscriminates bool             `json:"pipeline_discriminates"`
	Controls              []ControlOutcome `json:"controls"`
	Summary               string           `json:"summary"`
}

type controlDef struct {
	ID             string
	Kind           ControlKind
	Name           string
	Description    string
	Spec           Spec
	Observation    Observation
	Files          map[string]bool
	ExpectedPassed bool
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

// builtInControls is the frozen suite. Changing cases MUST bump SuiteID.
func builtInControls() []controlDef {
	return []controlDef{
		{
			ID:   "reference-repair",
			Kind: ControlPositive,
			Name: "Reference Repair",
			Description: "Known-good observation: exit 0, expected stdout, and required file present. " +
				"A working evaluator must accept it.",
			Spec: Spec{
				ExitCodeZero:   boolPtr(true),
				StdoutContains: "Successfully",
				FileExists:     "out/done.txt",
			},
			Observation: Observation{
				ExitCode: intPtr(0),
				Stdout:   "Successfully installed package",
			},
			Files:          map[string]bool{"out/done.txt": true},
			ExpectedPassed: true,
		},
		{
			ID:   "no-repair",
			Kind: ControlNegative,
			Name: "No Repair",
			Description: "Broken observation: non-zero exit, missing stdout marker, missing file. " +
				"A working evaluator must reject it.",
			Spec: Spec{
				ExitCodeZero:   boolPtr(true),
				StdoutContains: "Successfully",
				FileExists:     "out/done.txt",
			},
			Observation: Observation{
				ExitCode: intPtr(1),
				Stdout:   "error: command failed",
			},
			Files:          map[string]bool{"out/done.txt": false},
			ExpectedPassed: false,
		},
		{
			ID:          "stdout-miss",
			Kind:        ControlNegative,
			Name:        "Stdout Shortcut",
			Description: "Exit 0 but stdout lacks the required marker — anti-shortcut negative control.",
			Spec: Spec{
				ExitCodeZero:   boolPtr(true),
				StdoutContains: "Successfully",
			},
			Observation: Observation{
				ExitCode: intPtr(0),
				Stdout:   "ok",
			},
			ExpectedPassed: false,
		},
		{
			ID:          "nonzero-required",
			Kind:        ControlPositive,
			Name:        "Expected Failure Exit",
			Description: "Spec requires non-zero exit; observation has exit 2. Evaluator must accept.",
			Spec:        Spec{ExitCodeZero: boolPtr(false)},
			Observation: Observation{ExitCode: intPtr(2)},
			ExpectedPassed: true,
		},
	}
}

// SuiteFingerprint returns a stable digest of the frozen control definitions.
func SuiteFingerprint() string {
	type wire struct {
		SuiteID  string `json:"suite_id"`
		Controls []struct {
			ID             string      `json:"id"`
			Kind           ControlKind `json:"kind"`
			Name           string      `json:"name"`
			Description    string      `json:"description"`
			Spec           Spec        `json:"spec"`
			Observation    Observation `json:"observation"`
			Files          map[string]bool `json:"files,omitempty"`
			ExpectedPassed bool        `json:"expected_passed"`
		} `json:"controls"`
	}
	var w wire
	w.SuiteID = SuiteID
	for _, c := range builtInControls() {
		w.Controls = append(w.Controls, struct {
			ID             string      `json:"id"`
			Kind           ControlKind `json:"kind"`
			Name           string      `json:"name"`
			Description    string      `json:"description"`
			Spec           Spec        `json:"spec"`
			Observation    Observation `json:"observation"`
			Files          map[string]bool `json:"files,omitempty"`
			ExpectedPassed bool        `json:"expected_passed"`
		}{
			ID: c.ID, Kind: c.Kind, Name: c.Name, Description: c.Description,
			Spec: c.Spec, Observation: c.Observation, Files: c.Files,
			ExpectedPassed: c.ExpectedPassed,
		})
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RunControls evaluates the frozen positive/negative suite against Evaluate.
// PipelineDiscriminates is true only when every control is consistent with expectation
// and the suite contains at least one passing positive and one failing negative observation.
func RunControls() ControlReport {
	defs := builtInControls()
	outcomes := make([]ControlOutcome, 0, len(defs))
	allConsistent := true
	var sawPosOK, sawNegReject bool

	for _, d := range defs {
		files := d.Files
		var probe FileProbe
		if d.Spec.FileExists != "" {
			probe = func(path string) (bool, error) {
				if files == nil {
					return false, nil
				}
				return files[path], nil
			}
		}
		result := Evaluate(d.Spec, d.Observation, probe)
		consistent := result.Passed == d.ExpectedPassed
		if !consistent {
			allConsistent = false
		}
		if d.Kind == ControlPositive && result.Passed && consistent {
			sawPosOK = true
		}
		if d.Kind == ControlNegative && !result.Passed && consistent {
			sawNegReject = true
		}
		outcomes = append(outcomes, ControlOutcome{
			ID:             d.ID,
			Kind:           d.Kind,
			Name:           d.Name,
			Description:    d.Description,
			ExpectedPassed: d.ExpectedPassed,
			ObservedPassed: result.Passed,
			Consistent:     consistent,
			Result:         result,
		})
	}

	discriminates := allConsistent && sawPosOK && sawNegReject
	summary := "pipeline discriminates: positive controls accepted, negative controls rejected"
	if !discriminates {
		summary = "pipeline control suite FAILED — evaluator did not match frozen expectations"
	}

	return ControlReport{
		SuiteID:               SuiteID,
		Fingerprint:           SuiteFingerprint(),
		CreatedAt:             time.Now().UTC(),
		Passed:                discriminates,
		PipelineDiscriminates: discriminates,
		Controls:              outcomes,
		Summary:               summary,
	}
}

// ControlCasesPublic returns the frozen cases without running them (for docs/debug).
func ControlCasesPublic() []ControlCase {
	defs := builtInControls()
	out := make([]ControlCase, 0, len(defs))
	for _, d := range defs {
		out = append(out, ControlCase{
			ID:             d.ID,
			Kind:           d.Kind,
			Name:           d.Name,
			Description:    d.Description,
			Spec:           d.Spec,
			Observation:    d.Observation,
			FileExistsMap:  d.Files,
			ExpectedPassed: d.ExpectedPassed,
		})
	}
	return out
}
