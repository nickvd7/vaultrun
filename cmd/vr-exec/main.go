// Command vr-exec is the run supervisor on its own (see internal/supervise).
// The API installs its own binary into sandboxes instead; this one is for
// tests and for operators who want to try the supervisor directly.
package main

import (
	"os"

	"github.com/nickvd7/vaultrun/internal/supervise"
	"github.com/nickvd7/vaultrun/internal/xfsquota"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "xfs-quota" {
		os.Exit(xfsquota.Main(os.Args[2:]))
	}
	os.Exit(supervise.Main(os.Args[1:]))
}
