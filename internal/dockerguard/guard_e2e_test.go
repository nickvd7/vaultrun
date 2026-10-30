//go:build e2e

package dockerguard_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/google/uuid"

	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/dockerguard"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

const testImage = "python:3.12-slim"

// TestGuardE2E runs a sandbox through the guard the way the API does
// (workspace, supervisor, create, exec, stop, remove) and checks that the
// requests that would give the API the host are refused.
//
// Needs Docker with testImage. VAULTRUN_TEST_RUNTIME=runsc runs the sandbox
// under gVisor; VAULTRUN_TEST_SUPERVISOR (CGO_ENABLED=0 go build ./cmd/vr-exec)
// installs the run supervisor through the guard too.
func TestGuardE2E(t *testing.T) {
	ctx := context.Background()
	up, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("docker: %v", err)
	}
	if _, err := up.Ping(ctx); err != nil {
		t.Skipf("docker: %v", err)
	}
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	t.Setenv("DOCKER_RUNTIME", runtime)
	seccomp, err := dockerpkg.SeccompFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	netpolicy := "vaultrun-guard-test-np-" + uuid.NewString()[:8]
	g, err := dockerguard.New("/var/run/docker.sock",
		dockerguard.Policy{WorkspaceDir: base, Runtime: runtime, Seccomp: seccomp, Netpolicy: netpolicy},
		&dockerguard.Dirs{Base: base, UID: os.Getuid(), GID: os.Getgid()})
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "guard.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: g}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	// The API side: a normal client pointed at the guard.
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	dc, err := dockerpkg.New()
	if err != nil {
		t.Fatal(err)
	}
	if !dc.Guarded(ctx) {
		t.Fatal("client does not see the guard")
	}
	if sup := os.Getenv("VAULTRUN_TEST_SUPERVISOR"); sup != "" {
		dc.SetSupervisorBinary(sup)
	}
	ws := workspace.New(base)
	ws.SetDirOps(dc)
	sid := uuid.New()
	wsPath, err := ws.Create(sid)
	if err != nil {
		t.Fatalf("create workspace through the guard: %v", err)
	}
	if fi, err := os.Lstat(wsPath); err != nil || !fi.IsDir() {
		t.Fatalf("workspace not created: %v", err)
	}

	id, err := dc.CreateSandbox(ctx, dockerpkg.SandboxConfig{
		SessionID: sid, Image: testImage, WorkspacePath: wsPath,
		CPULimit: 0.5, MemoryLimitMB: 256, ContainerName: "vaultrun-guard-test-" + sid.String(),
	})
	if err != nil {
		t.Fatalf("CreateSandbox through the guard: %v", err)
	}
	t.Cleanup(func() { _ = up.ContainerRemove(context.Background(), id, container.RemoveOptions{Force: true}) })
	res, err := dc.Exec(ctx, dockerpkg.ExecConfig{ContainerID: id, Command: "sh",
		Args: []string{"-c", "echo guarded > /workspace/f && cat /workspace/f"}, TimeoutSeconds: 30})
	if err != nil || strings.TrimSpace(res.Stdout) != "guarded" {
		t.Fatalf("exec through the guard: %v %+v", err, res)
	}
	if _, err := dc.ContainerLogs(ctx, id, 10); err != nil {
		t.Fatalf("logs through the guard: %v", err)
	}

	// Containers the guard did not make: a plain one, and a stand-in for
	// the netpolicy helper.
	other := startPlain(t, up, "")
	np := startPlain(t, up, netpolicy)

	raw := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	if err := os.Symlink("/", filepath.Join(base, uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	link := ""
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			link = filepath.Join(base, e.Name())
		}
	}
	sandboxOpts := `"Labels":{"vaultrun.managed":"true"}`
	hostOpts := `"SecurityOpt":["no-new-privileges"],"Runtime":"` + runtime + `"`
	refused := []struct{ name, method, path, body string }{
		{"privileged", "POST", "/v1.43/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"Privileged":true,` + hostOpts + `}}`},
		{"host root bind", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"Binds":["/:/host"],` + hostOpts + `}}`},
		{"host root mount", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"Mounts":[{"Type":"bind","Source":"/","Target":"/host"}],` + hostOpts + `}}`},
		{"symlinked workspace", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"Mounts":[{"Type":"bind","Source":"` + link + `","Target":"/host"}],` + hostOpts + `}}`},
		{"docker socket", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"Mounts":[{"Type":"bind","Source":"/var/run/docker.sock","Target":"/s"}],` + hostOpts + `}}`},
		{"host pid", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"PidMode":"host",` + hostOpts + `}}`},
		{"host network", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"NetworkMode":"host",` + hostOpts + `}}`},
		{"capability", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"CapAdd":["SYS_ADMIN"],` + hostOpts + `}}`},
		{"lowercase privileged", "POST", "/containers/create", `{"Image":"` + testImage + `",` + sandboxOpts + `,"HostConfig":{"privileged":true,` + hostOpts + `}}`},
		{"exec in other container", "POST", "/containers/" + other + "/exec", `{"Cmd":["id"]}`},
		{"inspect other container", "GET", "/containers/" + other + "/json", ""},
		{"remove other container", "DELETE", "/containers/" + other + "?force=1", ""},
		{"archive from other container", "GET", "/containers/" + other + "/archive?path=/etc", ""},
		{"shell in netpolicy", "POST", "/containers/" + netpolicy + "/exec", `{"Cmd":["sh","-c","id"]}`},
		{"flush host firewall", "POST", "/containers/" + netpolicy + "/exec", `{"Cmd":["iptables","-F","INPUT"]}`},
		{"privileged exec in sandbox", "POST", "/containers/" + id + "/exec", `{"Cmd":["id"],"Privileged":true}`},
		{"unknown exec", "POST", "/exec/" + strings.Repeat("a", 64) + "/start", `{}`},
		{"list containers", "GET", "/containers/json", ""},
		{"volume on host path", "POST", "/volumes/create", `{"Name":"vaultrun-tools","DriverOpts":{"type":"none","o":"bind","device":"/"}}`},
		{"other volume", "POST", "/volumes/create", `{"Name":"evil"}`},
		{"build", "POST", "/build", ""},
		{"import image", "POST", "/images/create?fromSrc=-", ""},
		{"traversal", "GET", "/containers/x/../../info", ""},
		{"workspace traversal", "DELETE", "/vaultrun/workspaces/..", ""},
		{"remove base", "DELETE", "/vaultrun/workspaces/snapshots", ""},
	}
	for _, tc := range refused {
		req, _ := http.NewRequest(tc.method, "http://docker"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: HTTP %d, want 403", tc.name, resp.StatusCode)
		}
	}
	// The netpolicy helper takes VaultRun's own rules.
	resp, err := raw.Post("http://docker/containers/"+np+"/exec", "application/json",
		strings.NewReader(`{"Cmd":["iptables","-N","vr-0123456789ab"],"AttachStdout":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("iptables exec in netpolicy: HTTP %d", resp.StatusCode)
	}
	if err := dc.StopSandbox(ctx, id); err != nil {
		t.Fatalf("StopSandbox through the guard: %v", err)
	}
	if err := ws.Delete(sid); err != nil {
		t.Fatalf("remove workspace through the guard: %v", err)
	}
	if _, err := os.Lstat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace still there: %v", err)
	}
	t.Logf("runtime=%s", runtime)
}

func startPlain(t *testing.T, up *client.Client, name string) string {
	t.Helper()
	ctx := context.Background()
	resp, err := up.ContainerCreate(ctx, &container.Config{Image: testImage, Cmd: []string{"sleep", "300"}},
		&container.HostConfig{NetworkMode: "none"}, nil, nil, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = up.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true}) })
	if err := up.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	return resp.ID
}
