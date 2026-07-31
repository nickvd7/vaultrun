package nlpolicy

import (
	"errors"
	"strings"
	"testing"
)

// TestNaturalLanguageBoundsInput covers the free-text field that is forwarded
// verbatim to an LLM.
//
// Without a bound, one authenticated caller can drive the deployment's inference
// spend and hold an API worker open for the length of a very long completion —
// a cost and availability problem rather than a code-injection one, but reachable
// with a single request.
func TestNaturalLanguageBoundsInput(t *testing.T) {
	if err := ValidateNaturalLanguage(strings.Repeat("a", MaxNaturalLanguageLen+1)); err == nil {
		t.Error("a description one character over the limit was accepted")
	} else if !errors.Is(err, ErrDescriptionTooLong) {
		t.Errorf("oversized description returned %v, want ErrDescriptionTooLong", err)
	}

	// A megabyte prompt is the realistic version of the same attack.
	if err := ValidateNaturalLanguage(strings.Repeat("allow github.com ", 100_000)); err == nil {
		t.Error("a one-megabyte description was accepted")
	}

	if err := ValidateNaturalLanguage(strings.Repeat("a", MaxNaturalLanguageLen)); err != nil {
		t.Errorf("a description exactly at the limit was rejected: %v", err)
	}
}

// TestNaturalLanguageRejectsEmptyInput: an empty or whitespace-only description
// has nothing to parse, and sending it to the LLM wastes a call to get back a
// policy the caller did not describe.
func TestNaturalLanguageRejectsEmptyInput(t *testing.T) {
	for _, in := range []string{"", " ", "\t\n", strings.Repeat(" ", 100)} {
		if err := ValidateNaturalLanguage(in); err == nil {
			t.Errorf("ValidateNaturalLanguage(%q) accepted an empty description", in)
		}
	}
}

// TestNaturalLanguageAcceptsRealPolicies keeps the bound from rejecting the
// prompts the feature exists to serve, including ones that carry characters a
// naive filter might strip.
func TestNaturalLanguageAcceptsRealPolicies(t *testing.T) {
	cases := []string{
		"Allow github.com and pypi.org, max 2 CPU, 4GB RAM",
		"Block all network access except api.internal.example:8443",
		"Read-only access to /workspace; deny writes to /etc and /usr",
		"Sta alleen uitgaand verkeer naar github.com toe",     // non-English
		"allow 10.0.0.0/8 & 192.168.1.1; deny everything else", // punctuation
		strings.Repeat("allow example.com. ", 100),
	}

	for _, in := range cases {
		if err := ValidateNaturalLanguage(in); err != nil {
			t.Errorf("ValidateNaturalLanguage(%q) rejected a legitimate policy: %v", truncateForLog(in), err)
		}
	}
}

func truncateForLog(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:60] + "…"
}
