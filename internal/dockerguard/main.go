package dockerguard

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nickvd7/vaultrun/internal/docker"
)

// DefaultSocket is where the guard listens; the API reaches it with
// DOCKER_HOST=unix://<it>.
const DefaultSocket = "/run/vaultrun-docker/docker.sock"

// Main runs the guard ("api docker-guard") or, with "health", checks that one
// answers on its socket. It returns the exit status. Settings come from the
// environment:
//
//	VAULTRUN_GUARD_SOCKET  socket to serve (DefaultSocket)
//	DOCKER_SOCKET          the daemon's socket (/var/run/docker.sock)
//	WORKSPACE_HOST_DIR     workspace base directory, required; must belong to
//	                       root and not be writable by others
//	DOCKER_RUNTIME, DOCKER_SECCOMP_PROFILE  as for the API
//	NETPOLICY_CONTAINER    the container iptables may run in
//	VAULTRUN_API_UID, VAULTRUN_API_GID  owner of the socket and of new
//	                       workspace directories (65532)
//	VAULTRUN_TOOL_IMAGE    image for the XFS quota tool (default: the guard's own)
func Main(args []string) int {
	sock := envOr("VAULTRUN_GUARD_SOCKET", DefaultSocket)
	if len(args) > 0 && args[0] == "health" {
		return health(sock)
	}
	if err := run(sock); err != nil {
		fmt.Fprintln(os.Stderr, "docker-guard:", err)
		return 1
	}
	return 0
}

func run(sock string) error {
	base := strings.TrimRight(os.Getenv("WORKSPACE_HOST_DIR"), "/")
	if base == "" || !filepath.IsAbs(base) {
		return errors.New("WORKSPACE_HOST_DIR must be set to an absolute path")
	}
	fi, err := os.Stat(base)
	if err != nil {
		return err
	}
	if err := checkBaseOwner(base, fi); err != nil {
		return err
	}
	seccomp, err := docker.SeccompFromEnv()
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(envOr("VAULTRUN_API_UID", "65532"))
	if err != nil {
		return fmt.Errorf("VAULTRUN_API_UID: %w", err)
	}
	gid, err := strconv.Atoi(envOr("VAULTRUN_API_GID", "65532"))
	if err != nil {
		return fmt.Errorf("VAULTRUN_API_GID: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dirs := &Dirs{Base: base, UID: uid, GID: gid, ToolImage: os.Getenv("VAULTRUN_TOOL_IMAGE"), ToolBinary: self}
	policy := Policy{
		WorkspaceDir: base,
		Runtime:      strings.TrimSpace(os.Getenv("DOCKER_RUNTIME")),
		Seccomp:      seccomp,
		Netpolicy:    os.Getenv("NETPOLICY_CONTAINER"),
	}
	g, err := New(envOr("DOCKER_SOCKET", "/var/run/docker.sock"), policy, dirs)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if dirs.ToolImage == "" {
		// The guard's own image: in a container the hostname is its ID.
		if host, err := os.Hostname(); err == nil {
			if info, err := g.up.ContainerInspect(ctx, host); err == nil {
				dirs.ToolImage = info.Image
			}
		}
	}

	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chown(sock, uid, gid); err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	srv := &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("docker guard: serving", "socket", sock, "workspaces", base, "runtime", policy.Runtime, "netpolicy", policy.Netpolicy)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func health(sock string) int {
	c := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}
	resp, err := c.Get("http://guard" + ProbePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	resp, err = c.Get("http://guard/_ping")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
