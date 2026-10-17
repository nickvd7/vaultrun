// Package sandboxuser decides which user sandbox processes run as and the mode
// of session directories that follows from it. It has no platform-specific
// dependencies so every binary that talks to Docker can import it.
package sandboxuser

import (
	"fmt"
	"os"
	"strings"
)

// User returns the user ("uid:gid" or a name) sandbox processes run as.
//
// By default sandboxes run as the same uid:gid as the API process. They then
// own the bind-mounted session directory, which can stay 0o700. If the API runs
// as root (not the Compose default) there is no private uid to share, so the
// legacy "nobody" user is used together with world-writable (0o777) session
// directories. SANDBOX_USER overrides the choice; any value other than the API's
// own uid:gid keeps the 0o777 directories because the user cannot be assumed to
// own them.
func User() string {
	if v := strings.TrimSpace(os.Getenv("SANDBOX_USER")); v != "" {
		return v
	}
	if os.Getuid() != 0 {
		return ownUser()
	}
	return "nobody"
}

func ownUser() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// PrivateDirs reports whether session directories are owned by the sandbox user
// and can therefore be created owner-only.
func PrivateDirs() bool {
	return User() == ownUser()
}

// DirMode is the mode for session directories: 0o700 when the sandbox user owns
// them, otherwise 0o777 so a differently-numbered sandbox user (e.g. "nobody",
// UID 65534) can write. In the 0o777 case the parent WORKSPACE_BASE_DIR is
// created 0o700 so other host users cannot reach the session directories.
func DirMode() os.FileMode {
	if PrivateDirs() {
		return 0o700
	}
	return 0o777
}
