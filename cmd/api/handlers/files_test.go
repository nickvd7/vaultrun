package handlers

import "testing"

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "file"},
		{"plain", "report.txt", "report.txt"},
		{"spaces become underscores", "my report (final).txt", "my_report__final_.txt"},
		{"path separators stripped", "../../etc/passwd", ".._.._etc_passwd"},
		{"quotes and semicolons", `evil"; rm -rf /`, "evil___rm_-rf__"},
		{"crlf header injection", "name\r\nX-Injected: 1", "name__X-Injected__1"},
		{"unicode replaced", "résumé.pdf", "r_sum_.pdf"},
		{"all dots and underscores", "...", "file"},
		{"all underscores", "____", "file"},
		{"dots and underscores mixed", "._._.", "file"},
		{"leading dot kept for normal name", ".bashrc", ".bashrc"},
		{"hyphens and underscores kept", "my-file_name.tar.gz", "my-file_name.tar.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeFilename(tc.in); got != tc.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeFilenameOnlyAllowsSafeCharset(t *testing.T) {
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_"
	in := "a b!c@d#e$f%g^h&i*j(k)l"
	got := sanitizeFilename(in)
	for _, r := range got {
		found := false
		for _, a := range allowed {
			if r == a {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("sanitizeFilename(%q) = %q contains disallowed rune %q", in, got, r)
		}
	}
}

func TestDetectContentType(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"report.json", "application/json"},
		{"index.html", "text/html; charset=utf-8"},
		{"archive.tar.gz", "application/gzip"},
		{"plain.txt", "text/plain; charset=utf-8"},
		{"noext", "application/octet-stream"},
		{"weird.unknownext12345", "application/octet-stream"},
		{"/a/b/c/script.js", "text/javascript; charset=utf-8"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := detectContentType(tc.path); got != tc.want {
				t.Errorf("detectContentType(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
