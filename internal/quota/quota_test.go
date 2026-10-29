package quota

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/workspace"
)

// Size counts regular files only and never follows a symlink out of the
// workspace (a sandbox could otherwise make its workspace look empty or huge).
func TestSizeCountsOnlyWorkspaceFiles(t *testing.T) {
	base := t.TempDir()
	ws := workspace.New(base)
	id := uuid.New()
	dir, err := ws.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a"), make([]byte, 1000), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b"), make([]byte, 24), 0o644)
	outside := filepath.Join(base, "outside")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "big"), make([]byte, 1<<20), 0o644)
	os.Symlink(outside, filepath.Join(dir, "link"))

	n, err := Size(ws, id)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1024 {
		t.Fatalf("Size = %d, want 1024", n)
	}
	if n, err := Size(ws, uuid.New()); err != nil || n != 0 {
		t.Fatalf("missing workspace: %d %v", n, err)
	}
}
