//go:build unix

package dockerguard

import (
	"fmt"
	"os"
	"syscall"
)

// checkBaseOwner makes sure the API cannot create, rename or remove entries
// in the workspace base directory.
func checkBaseOwner(base string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must belong to root and not be writable by group or others (it is %v): the API must not be able to replace session directories", base, fi.Mode().Perm())
	}
	return nil
}
