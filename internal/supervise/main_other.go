//go:build !unix

package supervise

import (
	"fmt"
	"os"
)

// Main is only available on Unix; sandboxes are Linux containers.
func Main([]string) int {
	fmt.Fprintln(os.Stderr, "vr-exec: only supported on Unix")
	return 2
}
