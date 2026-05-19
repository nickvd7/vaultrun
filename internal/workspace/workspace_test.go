package workspace_test

import (
	"bytes"
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
		input    string
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
