// Package verify evaluates lightweight post-step / post-run assertions
// (exit code, stdout substring, file presence) used by Missions and MCP.
package verify

import (
	"fmt"
	"strings"
)

// Spec is a set of optional assertions. Empty fields are skipped.
type Spec struct {
	ExitCodeZero   *bool  `json:"exit_code_zero,omitempty"`
	StdoutContains string `json:"stdout_contains,omitempty"`
	FileExists     string `json:"file_exists,omitempty"`
}

// Observation is the measured outcome to check against Spec.
type Observation struct {
	ExitCode *int   `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// FileProbe reports whether a workspace-relative path exists.
// May be nil when Spec.FileExists is empty.
type FileProbe func(path string) (exists bool, err error)

// Check is one assertion outcome.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// Result aggregates all checks.
type Result struct {
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

// Empty reports whether the spec asks for no checks.
func (s Spec) Empty() bool {
	return s.ExitCodeZero == nil && s.StdoutContains == "" && s.FileExists == ""
}

// Evaluate runs Spec against Observation (and optional FileProbe).
func Evaluate(spec Spec, obs Observation, probe FileProbe) Result {
	var checks []Check

	if spec.ExitCodeZero != nil {
		wantZero := *spec.ExitCodeZero
		c := Check{Name: "exit_code_zero"}
		if obs.ExitCode == nil {
			c.Passed = false
			c.Detail = "exit_code missing"
		} else if wantZero && *obs.ExitCode == 0 {
			c.Passed = true
			c.Detail = "exit_code=0"
		} else if !wantZero && *obs.ExitCode != 0 {
			c.Passed = true
			c.Detail = fmt.Sprintf("exit_code=%d (non-zero as required)", *obs.ExitCode)
		} else {
			c.Passed = false
			c.Detail = fmt.Sprintf("exit_code=%d, want_zero=%v", *obs.ExitCode, wantZero)
		}
		checks = append(checks, c)
	}

	if spec.StdoutContains != "" {
		c := Check{Name: "stdout_contains"}
		if strings.Contains(obs.Stdout, spec.StdoutContains) {
			c.Passed = true
			c.Detail = fmt.Sprintf("found %q", spec.StdoutContains)
		} else {
			c.Passed = false
			c.Detail = fmt.Sprintf("substring %q not in stdout (%d bytes)", spec.StdoutContains, len(obs.Stdout))
		}
		checks = append(checks, c)
	}

	if spec.FileExists != "" {
		c := Check{Name: "file_exists"}
		if probe == nil {
			c.Passed = false
			c.Detail = "file probe unavailable (session_id required)"
		} else {
			exists, err := probe(spec.FileExists)
			if err != nil {
				c.Passed = false
				c.Detail = err.Error()
			} else if exists {
				c.Passed = true
				c.Detail = fmt.Sprintf("%q exists", spec.FileExists)
			} else {
				c.Passed = false
				c.Detail = fmt.Sprintf("%q not found", spec.FileExists)
			}
		}
		checks = append(checks, c)
	}

	passed := true
	if len(checks) == 0 {
		passed = true
	} else {
		for _, ch := range checks {
			if !ch.Passed {
				passed = false
				break
			}
		}
	}
	return Result{Passed: passed, Checks: checks}
}
