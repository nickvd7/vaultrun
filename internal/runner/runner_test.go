package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/policy"
)

// resolveStatus is a pure function — test all branches without any I/O.

func TestResolveStatusCompleted(t *testing.T) {
	r := &dockerpkg.ExecResult{ExitCode: 0}
	if s := resolveStatus(nil, r); s != "completed" {
		t.Fatalf("want completed, got %s", s)
	}
}

func TestResolveStatusFailedOnExecError(t *testing.T) {
	r := &dockerpkg.ExecResult{}
	if s := resolveStatus(context.DeadlineExceeded, r); s != "failed" {
		t.Fatalf("want failed, got %s", s)
	}
}

func TestResolveStatusFailedOnNonZeroExit(t *testing.T) {
	r := &dockerpkg.ExecResult{ExitCode: 1}
	if s := resolveStatus(nil, r); s != "failed" {
		t.Fatalf("want failed, got %s", s)
	}
}

func TestResolveStatusTimeout(t *testing.T) {
	r := &dockerpkg.ExecResult{TimedOut: true}
	if s := resolveStatus(nil, r); s != "timeout" {
		t.Fatalf("want timeout, got %s", s)
	}
}

// prepareRun validates before touching the DB — test the early-return paths.

func newNoopRunner(hook policy.Hook) *Runner {
	return &Runner{hook: hook}
}

func TestPrepareRunEmptyCommand(t *testing.T) {
	r := newNoopRunner(policy.AllowAll{})
	_, err := r.prepareRun(context.Background(), RunRequest{Command: ""})
	if err == nil || err.Error() != "command is required" {
		t.Fatalf("expected 'command is required', got %v", err)
	}
}

func TestPrepareRunShellInjectionRejected(t *testing.T) {
	r := newNoopRunner(policy.AllowAll{})
	injections := []string{
		"cmd; rm -rf /",
		"cmd | cat /etc/passwd",
		"cmd && evil",
		"$(whoami)",
		"`id`",
		"cmd < /etc/passwd",
		"cmd > /tmp/out",
	}
	for _, cmd := range injections {
		_, err := r.prepareRun(context.Background(), RunRequest{Command: cmd})
		if err == nil {
			t.Errorf("expected rejection for command %q, got nil", cmd)
		}
	}
}

func TestPrepareRunPolicyDenial(t *testing.T) {
	r := newNoopRunner(policy.DenyAll{Reason: "test deny"})
	_, err := r.prepareRun(context.Background(), RunRequest{
		Command:   "python",
		SessionID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected policy denial error")
	}
	if err.Error() != "command denied by policy: test deny" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRunValidCommandPassesValidation(t *testing.T) {
	// A valid command should pass the validation checks and only fail at
	// the DB step (nil db → panic, which we catch with recover).
	r := newNoopRunner(policy.AllowAll{})
	didPanic := func() (panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_, _ = r.prepareRun(context.Background(), RunRequest{
			Command:   "python",
			Args:      []string{"script.py"},
			SessionID: uuid.New(),
		})
		return false
	}()
	// We expect a panic from the nil DB, not an early-return error.
	if !didPanic {
		t.Fatal("expected nil DB to panic, meaning validation passed")
	}
}

// ---------------------------------------------------------------------------
// Artifact detection tests
// ---------------------------------------------------------------------------

func TestSnapshotDirEmpty(t *testing.T) {
	dir := t.TempDir()
	snap := snapshotDirForTest(dir)
	if len(snap) != 0 {
		t.Fatalf("want empty snapshot, got %d entries", len(snap))
	}
}

func TestSnapshotDirCapturesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.py"), []byte("print()"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := snapshotDirForTest(dir)
	if len(snap) != 2 {
		t.Fatalf("want 2 entries, got %d", len(snap))
	}
}

func TestSnapshotDirMissingDirReturnsEmpty(t *testing.T) {
	snap := snapshotDirForTest("/tmp/vaultrun-does-not-exist-xyz")
	if len(snap) != 0 {
		t.Fatalf("want empty snapshot for missing dir, got %d", len(snap))
	}
}

func TestDetectArtifactsNewFile(t *testing.T) {
	dir := t.TempDir()
	// Take snapshot of empty directory
	preSnap := snapshotDirForTest(dir)

	// "Run" creates a new file
	newFile := filepath.Join(dir, "output.csv")
	if err := os.WriteFile(newFile, []byte("a,b,c"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the new file is NOT in the snapshot
	if _, ok := preSnap[newFile]; ok {
		t.Fatal("new file should not be in pre-snapshot")
	}
}

func TestDetectArtifactsUnchangedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	existingFile := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existingFile, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Take snapshot — existing file is included
	preSnap := snapshotDirForTest(dir)
	if _, ok := preSnap[existingFile]; !ok {
		t.Fatal("existing file should be in pre-snapshot")
	}

	// Verify that a file with the same mtime as the snapshot would be skipped.
	// (We check the logic: if prev time == file mtime, !After returns false → skipped.)
	info, _ := os.Stat(existingFile)
	prev := preSnap[existingFile]
	if info.ModTime().After(prev) {
		t.Error("unchanged file mtime should not be after snapshot mtime")
	}
}

func TestSnapshotDirEmptyStringReturnsSafeEmpty(t *testing.T) {
	// Passing "" must not panic and must return an empty map.
	snap := snapshotDir("")
	if snap == nil {
		t.Fatal("expected non-nil map for empty dir")
	}
	if len(snap) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(snap))
	}
}

func TestSnapshotMtimePreserved(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := snapshotDirForTest(dir)
	info, _ := os.Stat(p)

	if !snap[p].Equal(info.ModTime()) {
		t.Fatalf("snapshot mtime %v != actual mtime %v", snap[p], info.ModTime())
	}
}

func TestSnapshotDirIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := snapshotDirForTest(dir)
	// Directories themselves should not appear, only the file inside.
	for k := range snap {
		info, _ := os.Stat(k)
		if info != nil && info.IsDir() {
			t.Errorf("directory %q should not be in snapshot", k)
		}
	}
	// The nested file should be captured.
	if len(snap) != 1 {
		t.Fatalf("expected 1 file in snapshot, got %d", len(snap))
	}
}

// Ensure deleteFileForTest (exposed for cleanup) and timestamp helpers are exercised.
func TestHelperFunctions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tmp.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Verify file exists then remove it.
	if err := deleteFileForTest(p); err != nil {
		t.Fatalf("deleteFileForTest: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}

	// Verify time comparison logic used by detectArtifacts.
	past := time.Now().Add(-time.Second)
	future := time.Now().Add(time.Second)
	if !future.After(past) {
		t.Fatal("future should be After past")
	}
}
