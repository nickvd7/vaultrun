package models

import "testing"

// TestStringArrayValueNeverNull pins the mapping of a nil slice to an empty
// Postgres array.
//
// Both columns bound to this type are NOT NULL DEFAULT '{}'. pq's own Value
// returns NULL for a nil slice, so before this was overridden a run submitted
// without arguments and a session created from a template with no allowed-host
// list both failed on a not-null violation, surfacing as a 500.
func TestStringArrayValueNeverNull(t *testing.T) {
	cases := []struct {
		name string
		in   StringArray
		want string
	}{
		{"nil", nil, "{}"},
		{"empty", StringArray{}, "{}"},
		{"one element", StringArray{"github.com"}, `{"github.com"}`},
		{"two elements", StringArray{"a", "b"}, `{"a","b"}`},
		{"element needing quotes", StringArray{`a,b"c`}, `{"a,b\"c"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := tc.in.Value()
			if err != nil {
				t.Fatalf("Value() error: %v", err)
			}
			if v == nil {
				t.Fatal("Value() returned NULL; the target columns reject it")
			}
			got, ok := v.(string)
			if !ok {
				t.Fatalf("Value() returned %T, want string", v)
			}
			if got != tc.want {
				t.Errorf("Value() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestStringArrayRoundTrip confirms Scan reverses Value, including for the
// empty array a nil slice now produces.
func TestStringArrayRoundTrip(t *testing.T) {
	for _, in := range []StringArray{nil, {}, {"one"}, {"one", "two"}, {"with space", `with "quote"`}} {
		v, err := in.Value()
		if err != nil {
			t.Fatalf("Value(%v): %v", in, err)
		}

		var out StringArray
		if err := out.Scan([]byte(v.(string))); err != nil {
			t.Fatalf("Scan(%v): %v", v, err)
		}
		if len(out) != len(in) {
			t.Errorf("round trip of %v gave %v", in, out)
			continue
		}
		for i := range in {
			if out[i] != in[i] {
				t.Errorf("round trip of %v gave %v", in, out)
				break
			}
		}
	}
}
