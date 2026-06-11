package models

import "testing"

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, need string
		want       bool
	}{
		{OrgRoleViewer, OrgRoleViewer, true},
		{OrgRoleExecutor, OrgRoleViewer, true},
		{OrgRoleAdmin, OrgRoleViewer, true},
		{OrgRoleAdmin, OrgRoleExecutor, true},
		{OrgRoleAdmin, OrgRoleAdmin, true},
		{OrgRoleViewer, OrgRoleExecutor, false},
		{OrgRoleViewer, OrgRoleAdmin, false},
		{OrgRoleExecutor, OrgRoleAdmin, false},
		{"unknown-role", OrgRoleViewer, false},
		{OrgRoleAdmin, "unknown-role", true},
		// Unknown role names rank as 0, so an unknown "have" only satisfies an
		// equally-unknown (i.e. also rank-0) "need".
		{"", "", true},
		{"unknown", "unknown", true},
		{"unknown", OrgRoleViewer, false},
	}
	for _, tc := range cases {
		if got := RoleAtLeast(tc.have, tc.need); got != tc.want {
			t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", tc.have, tc.need, got, tc.want)
		}
	}
}
