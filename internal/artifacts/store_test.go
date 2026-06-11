package artifacts

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStorePutGetDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalStore(dir)
	ctx := context.Background()

	content := []byte("hello artifact")
	key := "test-uuid-1234"

	// Put
	if err := store.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Verify file exists at expected path
	if _, err := os.Stat(filepath.Join(dir, key)); err != nil {
		t.Fatalf("file not found after Put: %v", err)
	}

	// Get
	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(content) {
		t.Fatalf("Get: want %q, got %q", content, got)
	}

	// Delete
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Get after delete must fail
	if _, err := store.Get(ctx, key); err == nil {
		t.Fatal("Get after Delete: expected error, got nil")
	}
}

func TestLocalStoreDeleteIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalStore(dir)
	ctx := context.Background()

	// Deleting a non-existent key must not return an error.
	if err := store.Delete(ctx, "nonexistent-key"); err != nil {
		t.Fatalf("Delete non-existent: want nil, got %v", err)
	}
}

func TestLocalStoreLegacyAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalStore(dir)
	ctx := context.Background()

	// Write a file at an absolute path outside baseDir to simulate a legacy record.
	tmpFile, err := os.CreateTemp("", "artifact-legacy-*")
	if err != nil {
		t.Fatal(err)
	}
	legacy := tmpFile.Name()
	t.Cleanup(func() { os.Remove(legacy) })
	if _, err := tmpFile.WriteString("legacy content"); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// Get using the absolute path as the key (legacy behaviour).
	rc, err := store.Get(ctx, legacy)
	if err != nil {
		t.Fatalf("Get legacy path: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "legacy content" {
		t.Fatalf("Get legacy path: want %q, got %q", "legacy content", got)
	}

	// Delete using the absolute path.
	if err := store.Delete(ctx, legacy); err != nil {
		t.Fatalf("Delete legacy path: %v", err)
	}
}

func TestLocalStorePutCleanupOnCopyError(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalStore(dir)
	ctx := context.Background()

	// Use an errReader that fails after some bytes to simulate a mid-copy failure.
	r := &errReader{data: []byte("partial"), failAt: 3}
	err := store.Put(ctx, "will-fail", r, 7, "application/octet-stream")
	if err == nil {
		t.Fatal("Put with failing reader: expected error, got nil")
	}
	// The partially-written file must be cleaned up.
	if _, statErr := os.Stat(filepath.Join(dir, "will-fail")); !os.IsNotExist(statErr) {
		t.Fatal("Put with failing reader: temp file was not cleaned up")
	}
}

// errReader reads from data until failAt bytes, then returns an error.
type errReader struct {
	data   []byte
	failAt int
	read   int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.read >= r.failAt {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.data[r.read:r.failAt])
	r.read += n
	return n, nil
}
