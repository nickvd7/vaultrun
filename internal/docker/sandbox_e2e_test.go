//go:build e2e

package docker

// Sandbox features that depend on the runtime, tested against a real Docker
// daemon under VAULTRUN_TEST_RUNTIME (empty = the daemon default, runc), so a
// failure under gVisor can be told apart from one that also happens under runc.
//
//	go test -c -tags e2e -o docker.test ./internal/docker/
//	sudo VAULTRUN_TEST_RUNTIME=runsc ./docker.test -test.run 'TestEgressAllowlistE2E|TestBrowserImageE2E' -test.v

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// hostCanReach reports whether this host has a direct route to addr, so an
// egress test does not mistake a network without internet for enforcement.
func hostCanReach(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 4*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// TestEgressAllowlistE2E: with allowed_hosts the sandbox reaches the listed IP
// and nothing else, and deleting the sandbox removes its iptables chain.
func TestEgressAllowlistE2E(t *testing.T) {
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	if os.Geteuid() != 0 || !EgressPolicyAvailable() {
		t.Skip("needs root and iptables (run the test binary with sudo)")
	}
	const allowed, blocked = "1.1.1.1", "8.8.8.8"
	if !hostCanReach(allowed+":443") || !hostCanReach(blocked+":443") {
		t.Skip("host has no direct internet access to " + allowed + " and " + blocked)
	}
	c := newRuntimeTestClient(t, runtime)
	ctx := context.Background()

	sessionID := uuid.New()
	id, err := c.CreateSandbox(ctx, SandboxConfig{
		SessionID:      sessionID,
		Image:          runtimeTestImage,
		WorkspacePath:  newRuntimeTestWorkspace(t),
		NetworkEnabled: true,
		AllowedHosts:   []string{allowed},
		CPULimit:       0.5,
		MemoryLimitMB:  256,
		ContainerName:  "vaultrun-egress-test-" + sessionID.String(),
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = c.StopSandbox(context.Background(), id)
		}
	})

	info, err := c.Inner().ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if runtime != "" && info.HostConfig.Runtime != runtime {
		t.Fatalf("container runtime = %q, want %q", info.HostConfig.Runtime, runtime)
	}

	connect := func(ip string) string {
		t.Helper()
		res, err := c.Exec(ctx, ExecConfig{
			ContainerID: id,
			Command:     "python",
			Args: []string{"-c", `import socket, sys
try:
    socket.create_connection((sys.argv[1], 443), timeout=5).close()
    print("OPEN")
except OSError as e:
    print("BLOCKED", e)`, ip},
			TimeoutSeconds: 30,
		})
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		return strings.TrimSpace(res.Stdout)
	}
	if got := connect(allowed); got != "OPEN" {
		t.Errorf("allowed host %s: %s", allowed, got)
	}
	if got := connect(blocked); !strings.HasPrefix(got, "BLOCKED") {
		t.Errorf("host %s outside allowed_hosts was reachable: %s", blocked, got)
	}

	chain := chainName(id)
	if err := exec.Command("iptables", "-S", chain).Run(); err != nil {
		t.Fatalf("egress chain %s not installed: %v", chain, err)
	}
	if err := c.StopSandbox(ctx, id); err != nil {
		t.Fatal(err)
	}
	stopped = true
	if err := exec.Command("iptables", "-S", chain).Run(); err == nil {
		t.Errorf("egress chain %s left behind after StopSandbox", chain)
	}
}

// TestEgressOpenInternetE2E: a sandbox with network but no allowed_hosts
// reaches the internet, but not a service listening on the host (through the
// bridge gateway, which iptables sees in INPUT) and not private ranges. The
// same host check runs with an allowlist, where it used to be reachable too.
func TestEgressOpenInternetE2E(t *testing.T) {
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	if os.Geteuid() != 0 || !EgressPolicyAvailable() {
		t.Skip("needs root and iptables (run the test binary with sudo)")
	}
	const public = "1.1.1.1"
	if !hostCanReach(public + ":443") {
		t.Skip("host has no direct internet access to " + public)
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	hostPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	c := newRuntimeTestClient(t, runtime)
	ctx := context.Background()
	for _, allowed := range [][]string{nil, {public}} {
		sessionID := uuid.New()
		id, err := c.CreateSandbox(ctx, SandboxConfig{
			SessionID:      sessionID,
			Image:          runtimeTestImage,
			WorkspacePath:  newRuntimeTestWorkspace(t),
			NetworkEnabled: true,
			AllowedHosts:   allowed,
			CPULimit:       0.5,
			MemoryLimitMB:  256,
			ContainerName:  "vaultrun-egress-open-test-" + sessionID.String(),
		})
		if err != nil {
			t.Fatalf("CreateSandbox(allowed=%v): %v", allowed, err)
		}
		connect := func(target, port string) string {
			t.Helper()
			res, err := c.Exec(ctx, ExecConfig{
				ContainerID: id,
				Command:     "python",
				Args: []string{"-c", `import socket, sys
host = sys.argv[1]
if host == "gateway":
    for line in open("/proc/net/route").read().splitlines()[1:]:
        f = line.split()
        if f[1] == "00000000":
            host = socket.inet_ntoa(bytes.fromhex(f[2])[::-1])
try:
    socket.create_connection((host, int(sys.argv[2])), timeout=5).close()
    print("OPEN")
except OSError as e:
    print("BLOCKED", e)`, target, port},
				TimeoutSeconds: 30,
			})
			if err != nil {
				t.Fatalf("exec: %v", err)
			}
			return strings.TrimSpace(res.Stdout)
		}
		if got := connect(public, "443"); got != "OPEN" {
			t.Errorf("allowed=%v: internet %s: %s", allowed, public, got)
		}
		if got := connect("gateway", hostPort); !strings.HasPrefix(got, "BLOCKED") {
			t.Errorf("allowed=%v: a service on the host was reachable through the gateway: %s", allowed, got)
		}
		if err := c.StopSandbox(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command("iptables", "-S", inputChain(chainName(id))).Run(); err == nil {
			t.Errorf("host-input chain left behind after StopSandbox")
		}
	}
}

// TestBrowserImageE2E renders a page with the Playwright image's Chromium
// inside a sandbox, with the same user, read-only rootfs and limits as a
// session.
func TestBrowserImageE2E(t *testing.T) {
	image := os.Getenv("VAULTRUN_TEST_BROWSER_IMAGE")
	if image == "" {
		t.Skip("VAULTRUN_TEST_BROWSER_IMAGE not set (build docker/browser-playwright-python.Dockerfile)")
	}
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	c := newRuntimeTestClient(t, runtime)
	ctx := context.Background()
	if ok, err := c.ImageExists(ctx, image); err != nil || !ok {
		t.Skipf("image %s not available locally", image)
	}

	ws := newRuntimeTestWorkspace(t)
	sessionID := uuid.New()
	id, err := c.CreateSandbox(ctx, SandboxConfig{
		SessionID:     sessionID,
		Image:         image,
		WorkspacePath: ws,
		CPULimit:      1,
		MemoryLimitMB: 1024,
		ContainerName: "vaultrun-browser-test-" + sessionID.String(),
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	t.Cleanup(func() { _ = c.StopSandbox(context.Background(), id) })

	res, err := c.Exec(ctx, ExecConfig{
		ContainerID: id,
		Command:     "python3",
		Args: []string{"-c", `from playwright.sync_api import sync_playwright
with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.set_content("<title>vaultrun</title><h1>hello</h1>")
    print(page.title())
    page.screenshot(path="/workspace/shot.png")
    browser.close()`},
		TimeoutSeconds: 120,
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "vaultrun" {
		t.Fatalf("chromium under runtime %q: exit=%d stdout=%q stderr=%s",
			runtime, res.ExitCode, res.Stdout, res.Stderr)
	}
	png, err := os.ReadFile(filepath.Join(ws, "shot.png"))
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("screenshot not written as PNG: %v", err)
	}
}
