//go:build e2e

package quota_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/quota"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

const testImage = "python:3.12-slim"

// TestXFSWorkspaceQuotaE2E puts a workspace base directory on a loop-mounted
// XFS filesystem with project quotas, applies the quota to a session's
// directory and writes past it from a sandbox: the write fails with "No
// space left on device" and the file stops at the limit.
//
// Needs root, a kernel with XFS quota support, mkfs.xfs, Docker and the
// supervisor binary in VAULTRUN_TEST_SUPERVISOR (CGO_ENABLED=0 go build
// ./cmd/vr-exec). VAULTRUN_TEST_RUNTIME=runsc runs the sandbox under gVisor.
func TestXFSWorkspaceQuotaE2E(t *testing.T) {
	supervisor := os.Getenv("VAULTRUN_TEST_SUPERVISOR")
	if os.Geteuid() != 0 || supervisor == "" {
		t.Skip("needs root and VAULTRUN_TEST_SUPERVISOR")
	}
	if _, err := exec.LookPath("mkfs.xfs"); err != nil {
		t.Skip("mkfs.xfs not installed")
	}
	dir := t.TempDir()
	img, base := filepath.Join(dir, "xfs.img"), filepath.Join(dir, "ws")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("truncate", "-s", "400M", img).CombinedOutput(); err != nil {
		t.Fatalf("truncate: %v %s", err, out)
	}
	if out, err := exec.Command("mkfs.xfs", "-q", img).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.xfs: %v %s", err, out)
	}
	// First, the tool container itself: on a filesystem without project
	// quotas Check must refuse (the API then refuses to start).
	t.Setenv("DOCKER_RUNTIME", os.Getenv("VAULTRUN_TEST_RUNTIME"))
	dc, err := dockerpkg.New()
	if err != nil {
		t.Skipf("docker: %v", err)
	}
	dc.SetSupervisorBinary(supervisor)
	ctx := context.Background()
	plain := &quota.XFS{Docker: dc, HostBaseDir: base, MaxBytes: 1 << 20, Image: testImage}
	if err := plain.Check(ctx); err == nil || !strings.Contains(err.Error(), "no project quotas") {
		t.Fatalf("Check on a directory without project quotas: %v", err)
	}

	if out, err := exec.Command("mount", "-o", "loop,prjquota", img, base).CombinedOutput(); err != nil {
		t.Skipf("cannot mount XFS with project quotas here: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", base).Run() })
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}

	var next atomic.Int64
	next.Store(100000)
	x := &quota.XFS{
		Docker:      dc,
		NextID:      func(context.Context) (int64, error) { return next.Add(1), nil },
		HostBaseDir: base,
		MaxBytes:    20 << 20,
		Image:       testImage,
	}
	if err := x.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}

	ws := workspace.New(base)
	sessionID := uuid.New()
	wsPath, err := ws.Create(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.Apply(ctx, sessionID); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	id, err := dc.CreateSandbox(ctx, dockerpkg.SandboxConfig{
		SessionID:     sessionID,
		Image:         testImage,
		WorkspacePath: wsPath,
		CPULimit:      0.5,
		MemoryLimitMB: 256,
		ContainerName: "vaultrun-quota-test-" + sessionID.String(),
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() { _ = dc.StopSandbox(context.Background(), id) })

	res, err := dc.Exec(ctx, dockerpkg.ExecConfig{
		ContainerID: id, Command: "sh",
		Args:           []string{"-c", "dd if=/dev/zero of=/workspace/big bs=1M count=60 conv=fsync; echo exit=$?"},
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	// XFS reports a full project quota as ENOSPC ("No space left on
	// device"), the way a full directory tree would look; EDQUOT is for
	// user and group quotas.
	if out := res.Stdout + res.Stderr; !strings.Contains(out, "No space left on device") && !strings.Contains(out, "quota exceeded") {
		t.Fatalf("writing 60 MB into a 20 MB workspace did not hit the quota: %+v", res)
	}
	info, err := os.Stat(filepath.Join(wsPath, "big"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 20<<20 {
		t.Fatalf("file grew to %d bytes past the 20 MB quota", info.Size())
	}
	t.Logf("write stopped at %d MB (runtime=%s)", info.Size()>>20, os.Getenv("VAULTRUN_TEST_RUNTIME"))
}
