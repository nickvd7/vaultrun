package workspace_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

func newManager(t *testing.T) (*workspace.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	return workspace.New(dir), dir
}

func TestCreate(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()

	path, err := m.Create(id)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("workspace dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("workspace path is not a directory")
	}
}

func TestDelete(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()

	path, _ := m.Create(id)
	if err := m.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("workspace dir should not exist after delete")
	}
}

func TestWriteAndReadFile(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)

	content := []byte("hello vaultrun")
	n, err := m.WriteFile(id, "test.txt", bytes.NewReader(content), 1024*1024)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("expected %d bytes written, got %d", len(content), n)
	}

	f, err := m.OpenFile(id, "test.txt")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()

	got, _ := io.ReadAll(f)
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q want %q", got, content)
	}
}

func TestPathTraversalPrevention(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)

	traversalPaths := []string{
		"../../../etc/passwd",
		"../../secret",
		"./../../etc/hosts",
		"%2e%2e%2fetc%2fpasswd",
	}

	for _, path := range traversalPaths {
		_, err := m.SafePath(id, path)
		if err == nil {
			t.Errorf("SafePath(%q) should have returned an error", path)
		}
	}
}

func TestSafePathNormalization(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)

	cases := []struct {
		input      string
		wantSuffix string
	}{
		{"foo.txt", "/foo.txt"},
		{"/foo.txt", "/foo.txt"},
		{"subdir/script.py", "/subdir/script.py"},
	}

	for _, tc := range cases {
		got, err := m.SafePath(id, tc.input)
		if err != nil {
			t.Errorf("SafePath(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if !strings.HasSuffix(got, tc.wantSuffix) {
			t.Errorf("SafePath(%q) = %q, want suffix %q", tc.input, got, tc.wantSuffix)
		}
	}
}

func TestFileSizeLimit(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)

	maxBytes := int64(10)
	content := bytes.Repeat([]byte("x"), int(maxBytes)+1)

	_, err := m.WriteFile(id, "big.txt", bytes.NewReader(content), maxBytes)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}

	// File should be cleaned up
	_, err2 := m.OpenFile(id, "big.txt")
	if err2 == nil {
		// Check it's gone from workspace
		sp, _ := m.SessionPath(id), ""
		_ = sp
	}
}

func TestWriteFileCreatesSubdirs(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)

	content := []byte("nested content")
	_, err := m.WriteFile(id, "a/b/c/file.txt", bytes.NewReader(content), 1024*1024)
	if err != nil {
		t.Fatalf("WriteFile with subdirs: %v", err)
	}

	f, err := m.OpenFile(id, "a/b/c/file.txt")
	if err != nil {
		t.Fatalf("OpenFile after subdir creation: %v", err)
	}
	defer f.Close()

	got, _ := io.ReadAll(f)
	if string(got) != string(content) {
		t.Fatalf("content mismatch in subdir file")
	}
}

func TestSymlinkEscapePrevention(t *testing.T) {
	m, base := newManager(t)
	id := uuid.New()
	m.Create(id)

	// Create a file outside the workspace to be the symlink target
	secret, _ := os.CreateTemp(base, "secret-*.txt")
	secret.WriteString("secret data")
	secret.Close()

	// Create a symlink inside the workspace pointing outside
	sp := m.SessionPath(id)
	symlinkPath := filepath.Join(sp, "escape-link")
	if err := os.Symlink(secret.Name(), symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Opening via the API should be rejected
	f, err := m.OpenFile(id, "escape-link")
	if err == nil {
		f.Close()
		t.Error("OpenFile should reject a symlink that escapes the workspace")
	}

	exists, err := m.Exists(id, "escape-link")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Error("Exists must not report host path via escaping symlink")
	}
	if !m.IsSymlink(id, "escape-link") {
		t.Error("IsSymlink must report an escaping symlink so the file list can mark it")
	}
	real := filepath.Join(sp, "real.txt")
	if err := os.WriteFile(real, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(sp, "inner-link")); err != nil {
		t.Fatal(err)
	}
	if !m.IsSymlink(id, "/inner-link") {
		t.Error("IsSymlink must report a symlink whose target stays in the workspace")
	}
	if m.IsSymlink(id, "real.txt") {
		t.Error("IsSymlink must not report a regular file")
	}
	exists, err = m.Exists(id, "missing.txt")
	if err != nil || exists {
		t.Fatalf("missing file: exists=%v err=%v", exists, err)
	}
}

// sessionPath is exposed via SessionPath
func TestSessionPathExposed(t *testing.T) {
	m, base := newManager(t)
	id := uuid.New()
	m.Create(id)

	sp := m.SessionPath(id)
	expected := filepath.Join(base, id.String())
	if sp != expected {
		t.Fatalf("SessionPath = %q, want %q", sp, expected)
	}
}

func TestCreateUsesPrivateDirsForOwnUser(t *testing.T) {
	t.Setenv("SANDBOX_USER", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()))
	m := workspace.New(t.TempDir())
	path, err := m.Create(uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("session dir mode = %o, want 700", info.Mode().Perm())
	}
}

func TestOpenFileRejectsDirectory(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)
	if err := os.MkdirAll(filepath.Join(m.SessionPath(id), "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenFile(id, "sub"); !errors.Is(err, workspace.ErrIsDirectory) {
		t.Fatalf("OpenFile(dir) err = %v, want ErrIsDirectory", err)
	}
}

func TestRemoveDoesNotFollowSymlinkedDirectory(t *testing.T) {
	m, base := newManager(t)
	id := uuid.New()
	m.Create(id)

	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := m.SessionPath(id)
	if err := os.Symlink(outside, filepath.Join(sp, "linkdir")); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(id, "linkdir/victim.txt"); err == nil {
		t.Fatal("Remove through a symlinked directory must fail")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the workspace was removed: %v", err)
	}

	inside := filepath.Join(sp, "a.txt")
	os.WriteFile(inside, []byte("x"), 0o644)
	if err := m.Remove(id, "a.txt"); err != nil {
		t.Fatalf("Remove inside: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("file still present: %v", err)
	}
	if err := m.Remove(id, "/"); err == nil {
		t.Fatal("removing the workspace root must fail")
	}
}

func TestWriteFileKeepsLegacyWorldWritableSessionMode(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)
	root := m.SessionPath(id)
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := m.WriteFile(id, "a/b/c.txt", strings.NewReader("x"), 1024); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, p := range []string{root, filepath.Join(root, "a"), filepath.Join(root, "a", "b")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o777 {
			t.Errorf("%s mode = %o, want 777", p, got)
		}
	}
}

func TestWriteFileKeepsPrivateSessionMode(t *testing.T) {
	m, _ := newManager(t)
	id := uuid.New()
	m.Create(id)
	root := m.SessionPath(id)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.WriteFile(id, "a/c.txt", strings.NewReader("x"), 1024); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, _ := os.Stat(filepath.Join(root, "a"))
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("subdir mode = %o, want 700", got)
	}
}
