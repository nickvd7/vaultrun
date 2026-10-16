package version

import (
	"regexp"
	"testing"
)

func TestVersionIsSemver(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version()) {
		t.Fatalf("VERSION = %q, want MAJOR.MINOR.PATCH", Version())
	}
}
