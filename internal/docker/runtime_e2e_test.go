//go:build e2e

package docker

// Runtime tests against a real Docker daemon. They run every sandbox under
// VAULTRUN_TEST_RUNTIME (CI registers gVisor's runsc) and check that the
// runtime is applied and the sandbox still works: exec, the workspace bind
// mount in both directions, and the hardening options.
//
//	VAULTRUN_TEST_RUNTIME=runsc go test -tags e2e ./internal/docker/ -run Runtime -v

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/sandboxuser"
)

const runtimeTestImage = "python:3.12-slim"

// newRuntimeTestClient returns a client configured for runtime, or skips when
// no daemon or image is available.
func newRuntimeTestClient(t *testing.T, runtime string) *Client {
	t.Helper()
	t.Setenv("DOCKER_RUNTIME", runtime)
	c, err := New()
	if err != nil {
		t.Skipf("docker client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Inner().Ping(ctx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	if ok, err := c.ImageExists(ctx, runtimeTestImage); err != nil || !ok {
		t.Skipf("image %s not available locally (pull it first)", runtimeTestImage)
	}
	return c
}

func newRuntimeTestWorkspace(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ws")
	if err := os.Mkdir(dir, sandboxuser.DirMode()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, sandboxuser.DirMode()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRuntimeSandboxE2E(t *testing.T) {
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	if runtime == "" {
		t.Skip("VAULTRUN_TEST_RUNTIME not set")
	}
	c := newRuntimeTestClient(t, runtime)
	ctx := context.Background()

	if err := c.CheckRuntime(ctx); err != nil {
		t.Fatalf("CheckRuntime(%q): %v", runtime, err)
	}

	ws := newRuntimeTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "from_host.txt"), []byte("host-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.New()
	id, err := c.CreateSandbox(ctx, SandboxConfig{
		SessionID:     sessionID,
		Image:         runtimeTestImage,
		WorkspacePath: ws,
		CPULimit:      0.5,
		MemoryLimitMB: 256,
		ContainerName: "vaultrun-rt-test-" + sessionID.String(),
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() { _ = c.StopSandbox(context.Background(), id) })

	info, err := c.Inner().ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if info.HostConfig.Runtime != runtime {
		t.Fatalf("container runtime = %q, want %q", info.HostConfig.Runtime, runtime)
	}
	// The runtime is an extra layer, not a replacement for the others.
	if !info.HostConfig.ReadonlyRootfs || len(info.HostConfig.CapDrop) != 1 || info.HostConfig.CapDrop[0] != "ALL" ||
		string(info.HostConfig.NetworkMode) != "none" {
		t.Errorf("hardening lost: readonly=%v capdrop=%v network=%s",
			info.HostConfig.ReadonlyRootfs, info.HostConfig.CapDrop, info.HostConfig.NetworkMode)
	}

	exec := func(args ...string) *ExecResult {
		t.Helper()
		res, err := c.Exec(ctx, ExecConfig{ContainerID: id, Command: args[0], Args: args[1:], TimeoutSeconds: 30})
		if err != nil {
			t.Fatalf("exec %v: %v", args, err)
		}
		return res
	}

	// Host → sandbox and sandbox → host through the workspace bind mount.
	if res := exec("cat", "/workspace/from_host.txt"); res.ExitCode != 0 || res.Stdout != "host-data" {
		t.Errorf("read host file: exit=%d stdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
	res := exec("python", "-c", "open('/workspace/from_sandbox.txt','w').write('sandbox-data')")
	if res.ExitCode != 0 {
		t.Fatalf("write from sandbox: exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if got, err := os.ReadFile(filepath.Join(ws, "from_sandbox.txt")); err != nil || string(got) != "sandbox-data" {
		t.Errorf("sandbox file on host = %q, %v", got, err)
	}
	// A file the host changes after the sandbox read it must not be served
	// stale (gVisor caches file data unless mounts are shared).
	if err := os.WriteFile(filepath.Join(ws, "from_host.txt"), []byte("host-data-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := exec("cat", "/workspace/from_host.txt"); res.Stdout != "host-data-v2" {
		t.Errorf("host update not visible in sandbox: %q", res.Stdout)
	}

	// Read-only root filesystem and non-zero exit codes still behave.
	if res := exec("touch", "/etc/vaultrun-probe"); res.ExitCode == 0 {
		t.Error("write to read-only root filesystem succeeded")
	}
	if res := exec("python", "-c", "import sys; sys.exit(3)"); res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}

	if runtime == "runsc" {
		// Under gVisor the sandbox sees the Sentry's kernel, not the host's.
		hostVersion, err := os.ReadFile("/proc/version")
		if err != nil {
			t.Fatal(err)
		}
		res := exec("cat", "/proc/version")
		if strings.TrimSpace(res.Stdout) == strings.TrimSpace(string(hostVersion)) {
			t.Errorf("sandbox /proc/version equals the host's — not running under gVisor: %q", res.Stdout)
		}
		t.Logf("sandbox kernel: %s", strings.TrimSpace(res.Stdout))
	}
}

func TestRuntimeUnknownIsRejectedE2E(t *testing.T) {
	c := newRuntimeTestClient(t, "vaultrun-no-such-runtime")
	ctx := context.Background()

	if err := c.CheckRuntime(ctx); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("CheckRuntime: want ErrRuntimeUnavailable, got %v", err)
	}
	// Even without the startup check, a sandbox must not fall back to the
	// default runtime.
	sessionID := uuid.New()
	id, err := c.CreateSandbox(ctx, SandboxConfig{
		SessionID:     sessionID,
		Image:         runtimeTestImage,
		WorkspacePath: newRuntimeTestWorkspace(t),
		CPULimit:      0.5,
		MemoryLimitMB: 128,
		ContainerName: "vaultrun-rt-test-" + sessionID.String(),
	})
	if err == nil {
		_ = c.StopSandbox(ctx, id)
		t.Fatal("CreateSandbox with an unknown runtime succeeded")
	}
}

// buildSupervisor compiles cmd/vr-exec as a static binary for the sandbox.
func buildSupervisor(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "vr-exec")
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/vr-exec")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build vr-exec: %v\n%s", err, b)
	}
	return out
}

// A command that runs past its timeout is stopped inside the sandbox together
// with the processes it started (Docker cannot kill an exec), while its
// output and the exit status of normal commands are unchanged.
func TestRuntimeTimeoutStopsCommandE2E(t *testing.T) {
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	c := newRuntimeTestClient(t, runtime)
	c.SetSupervisorBinary(buildSupervisor(t))
	ctx := context.Background()
	sessionID := uuid.New()
	id, err := c.CreateSandbox(ctx, SandboxConfig{
		SessionID:     sessionID,
		Image:         runtimeTestImage,
		WorkspacePath: newRuntimeTestWorkspace(t),
		CPULimit:      0.5,
		MemoryLimitMB: 256,
		ContainerName: "vaultrun-timeout-test-" + sessionID.String(),
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() { _ = c.StopSandbox(context.Background(), id) })

	countSleeps := func() int {
		t.Helper()
		res, err := c.Exec(ctx, ExecConfig{ContainerID: id, Command: "python", Args: []string{"-c", `import os
n = 0
for p in os.listdir("/proc"):
    if p.isdigit() and p != "1":
        try:
            if open(f"/proc/{p}/cmdline", "rb").read().split(b"\0")[0].endswith(b"sleep"):
                n += 1
        except OSError:
            pass
print(n)`}, TimeoutSeconds: 30})
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.Atoi(strings.TrimSpace(res.Stdout))
		return n
	}

	res, err := c.Exec(ctx, ExecConfig{ContainerID: id, Command: "sh", Args: []string{"-c", "echo started; sleep 300 & sleep 300"}, TimeoutSeconds: 2})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !res.TimedOut || res.ExitCode != 124 || !strings.Contains(res.Stdout, "started") || !strings.Contains(res.Stderr, "timed out") {
		t.Fatalf("timed-out run: %+v", res)
	}
	if n := countSleeps(); n != 0 {
		t.Fatalf("%d sleep processes still running after the timeout", n)
	}

	ok, err := c.Exec(ctx, ExecConfig{ContainerID: id, Command: "sh", Args: []string{"-c", "echo hi; exit 7"}, TimeoutSeconds: 10})
	if err != nil || ok.TimedOut || ok.ExitCode != 7 || strings.TrimSpace(ok.Stdout) != "hi" {
		t.Fatalf("normal run: %+v %v", ok, err)
	}
	missing, err := c.Exec(ctx, ExecConfig{ContainerID: id, Command: "no-such-command", TimeoutSeconds: 10})
	if err != nil || missing.ExitCode != 127 {
		t.Fatalf("missing command: %+v %v", missing, err)
	}
	if strings.Contains(runtime, "runsc") {
		t.Log("runtime=runsc")
	}
}
