package handlers

import "testing"

func TestDeniedSecret(t *testing.T) {
	cases := []struct {
		name      string
		allowed   []string
		requested []string
		want      string
		denied    bool
	}{
		{"nil allowlist is unrestricted", nil, []string{"A", "B"}, "", false},
		{"empty allowlist denies everything", []string{}, []string{"A"}, "A", true},
		{"all covered", []string{"A", "B"}, []string{"B"}, "", false},
		{"first uncovered is reported", []string{"A"}, []string{"A", "C", "D"}, "C", true},
		{"nothing requested", []string{}, nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, denied := deniedSecret(tc.allowed, tc.requested)
			if got != tc.want || denied != tc.denied {
				t.Fatalf("got (%q,%v), want (%q,%v)", got, denied, tc.want, tc.denied)
			}
		})
	}
}
