package localgateway

import "testing"

func TestSanitizeWorkspacePath(t *testing.T) {
	ok := []struct{ in, want string }{
		{"notes.txt", "notes.txt"},
		{"dir/file.py", "dir/file.py"},
		{"/abs/ok.txt", "abs/ok.txt"},
		{"./a/b", "a/b"},
	}
	for _, tc := range ok {
		got, err := sanitizeWorkspacePath(tc.in)
		if err != nil {
			t.Fatalf("sanitizeWorkspacePath(%q) err=%v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("sanitizeWorkspacePath(%q)=%q want %q", tc.in, got, tc.want)
		}
	}

	bad := []string{
		"",
		"..",
		"../etc/passwd",
		"foo/../../etc/passwd",
		"a\\b",
		"x\x00y",
		"/",
		".",
	}
	for _, in := range bad {
		if _, err := sanitizeWorkspacePath(in); err == nil {
			t.Fatalf("sanitizeWorkspacePath(%q) = nil, want error", in)
		}
	}
}
