package main

import "testing"

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
