package main

import (
	"context"
	"testing"
)

func TestParseStrictToolBool(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1", "yes"} {
		b, err := parseStrictToolBool(v)
		if err != nil || !b {
			t.Fatalf("%q: %v %v", v, b, err)
		}
	}
	for _, v := range []string{"false", "0", "no"} {
		b, err := parseStrictToolBool(v)
		if err != nil || b {
			t.Fatalf("%q: %v %v", v, b, err)
		}
	}
	if _, err := parseStrictToolBool("maybe"); err == nil {
		t.Fatal("expected error")
	}
}

func TestToolVerifyEvidenceRequiresSource(t *testing.T) {
	s := &server{}
	_, err := s.toolVerifyEvidence(context.Background(), map[string]string{})
	if err == nil {
		t.Fatal("expected error for empty args")
	}
	_, err = s.toolVerifyEvidence(context.Background(), map[string]string{
		"include_controls": "maybe",
	})
	if err == nil {
		t.Fatal("expected include_controls parse error")
	}
}

func TestVerifyEvidenceToolDefined(t *testing.T) {
	var found bool
	for _, tool := range verifyToolDefinitions() {
		if tool.Name == "verify_evidence" {
			found = true
			if tool.InputSchema.Properties["verification_id"].Type != "string" {
				t.Fatal("verification_id prop missing")
			}
		}
	}
	if !found {
		t.Fatal("verify_evidence tool missing")
	}
}
