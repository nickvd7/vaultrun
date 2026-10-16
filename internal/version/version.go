// Package version is the single place binaries learn which release they are.
//
// The number lives in the VERSION file next to this package and is embedded at
// build time, so `go build`, `go install` and the Docker image all agree
// without build arguments. scripts/check-versions.sh keeps it in step with the
// Python SDK, dashboard and site versions.
package version

import (
	_ "embed"
	"fmt"
	"runtime/debug"
	"strings"
)

//go:embed VERSION
var embedded string

// Commit can be set at build time (-ldflags "-X .../internal/version.Commit=abc123")
// to override the revision Go records from version control.
var Commit string

// Version is the release number, for example "0.4.3".
func Version() string {
	return strings.TrimSpace(embedded)
}

// Revision returns the short commit the binary was built from, or "" when the
// build carries none (for example a Docker build without .git).
func Revision() string {
	rev := Commit
	if rev == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" {
					rev = s.Value
				}
			}
		}
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	return rev
}

// String renders "0.4.3" or "0.4.3 (abc1234)".
func String() string {
	if rev := Revision(); rev != "" {
		return fmt.Sprintf("%s (%s)", Version(), rev)
	}
	return Version()
}
