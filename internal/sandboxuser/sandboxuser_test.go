package sandboxuser

import (
	"fmt"
	"os"
	"testing"
)

func TestUserAndDirMode(t *testing.T) {
	own := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())

	t.Setenv("SANDBOX_USER", "")
	if os.Getuid() != 0 {
		if got := User(); got != own {
			t.Errorf("default sandbox user = %q, want %q", got, own)
		}
		if DirMode() != 0o700 {
			t.Errorf("default dir mode = %o, want 700", DirMode())
		}
	}

	t.Setenv("SANDBOX_USER", "nobody")
	if DirMode() != 0o777 {
		t.Errorf("dir mode for foreign sandbox user = %o, want 777", DirMode())
	}

	t.Setenv("SANDBOX_USER", own)
	if DirMode() != 0o700 {
		t.Errorf("dir mode when sandbox user is the API user = %o, want 700", DirMode())
	}
}
