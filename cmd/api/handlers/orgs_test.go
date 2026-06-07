package handlers

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "Acme Corp", "acme-corp"},
		{"already a slug", "acme-corp", "acme-corp"},
		{"leading and trailing whitespace", "  Acme Corp  ", "acme-corp"},
		// Each ASCII special/space becomes its own dash (no collapsing of those
		// runs); only leftover non-ASCII runs get collapsed by the second pass.
		{"specials become individual dashes", "Acme & Co.,  Ltd!!", "acme---co----ltd"},
		{"strips leading and trailing dashes", "---Acme---", "acme"},
		// Non-ASCII letters survive the first (unicode-aware) pass but are then
		// stripped out by the [^a-z0-9-]+ cleanup regex.
		{"non-ascii letters stripped by cleanup pass", "Café Münster", "caf--m-nster"},
		{"digits kept", "Team42", "team42"},
		{"all specials yields empty slug", "!!!", ""},
		{"underscores become dashes", "my_org_name", "my-org-name"},
		{"mixed case folded", "MiXeD-CaSe", "mixed-case"},
		{"empty input", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slugify(tc.in); got != tc.want {
				t.Errorf("slugify(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSlugifyNeverProducesLeadingOrTrailingDash(t *testing.T) {
	inputs := []string{"-leading", "trailing-", "-both-", "--many--dashes--", "!start", "end!"}
	for _, in := range inputs {
		got := slugify(in)
		if len(got) == 0 {
			continue
		}
		if got[0] == '-' || got[len(got)-1] == '-' {
			t.Errorf("slugify(%q) = %q has leading/trailing dash", in, got)
		}
	}
}
