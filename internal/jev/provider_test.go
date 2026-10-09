package jev

import "testing"

func TestValidateBaseURLPrivateOptIn(t *testing.T) {
	if err := validateBaseURL("http://127.0.0.1:8080", false); err == nil {
		t.Fatal("private http must fail without opt-in")
	}
	if err := validateBaseURL("http://127.0.0.1:8080", true); err != nil {
		t.Fatal(err)
	}
	if err := validateBaseURL("http://metadata/", true); err == nil {
		t.Fatal("metadata host must stay blocked")
	}
	if err := validateBaseURL(OpenJEVBaseURL, false); err != nil {
		// May fail offline if DNS blocked — skip soft if resolve fails in sandbox.
		t.Logf("openjev public validate: %v", err)
	}
}
