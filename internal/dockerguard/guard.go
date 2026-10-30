// Package dockerguard sits between the API and the Docker daemon.
//
// Access to the Docker socket is root on the host: whoever can talk to it can
// start a privileged container with the host's filesystem mounted. The API
// needs Docker to run sandboxes, so a bug in the API (or anything it runs)
// that let an attacker make Docker calls would hand over the host.
//
// The guard is the only process with the Docker socket. It serves a socket of
// its own that speaks the Docker API, and forwards a request only when it is
// one the API makes, with the settings the API uses: sandboxes without
// privileges, capabilities, host namespaces or devices, under the configured
// runtime and seccomp profile, mounting only a session directory; commands
// only in VaultRun's own containers; iptables only on VaultRun's own chains.
// Everything else gets 403. The few things that do need privileges (creating
// and removing workspace directories, XFS quotas) are done by the guard
// itself, on its own terms, through /vaultrun/* endpoints.
package dockerguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"

	"github.com/nickvd7/vaultrun/internal/docker"
)

// ProbePath answers 200 on a guard and 404 on a Docker daemon.
const ProbePath = docker.GuardProbePath

// maxBody caps JSON request bodies (a seccomp profile is ~10 KB).
const maxBody = 4 << 20

// Guard is the filtering proxy.
type Guard struct {
	Policy Policy
	// Dirs creates and removes workspace directories and sets quotas.
	Dirs *Dirs

	up    *client.Client
	proxy *httputil.ReverseProxy

	mu    sync.Mutex
	execs map[string]time.Time // exec IDs created through the guard
}

// New returns a guard that forwards to the Docker daemon at the unix socket
// upstream.
func New(upstream string, policy Policy, dirs *Dirs) (*Guard, error) {
	up, err := client.NewClientWithOpts(client.WithHost("unix://"+upstream), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	target := &url.URL{Scheme: "http", Host: "docker"}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", upstream)
		},
		DisableCompression: true,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = "docker"
		},
		Transport:     tr,
		FlushInterval: -1, // logs, stats, wait and attach stream
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadGateway, fmt.Errorf("docker daemon: %w", err))
		},
	}
	if dirs != nil {
		dirs.up = up
	}
	return &Guard{Policy: policy, Dirs: dirs, up: up, proxy: proxy, execs: map[string]time.Time{}}, nil
}

var (
	versionRe   = regexp.MustCompile(`^/v[0-9]+(\.[0-9]+)?(/|$)`)
	containerRe = regexp.MustCompile(`^/containers/([^/]+)(?:/([a-z]+))?$`)
	execRe      = regexp.MustCompile(`^/exec/([0-9a-f]{64})/(start|resize|json)$`)
	imageRe     = regexp.MustCompile(`^/images/(.+)/json$`)
	networkRe   = regexp.MustCompile(`^/networks/([^/]+)$`)
	workspaceRe = regexp.MustCompile(`^/vaultrun/workspaces/([^/]+)$`)
)

// Container operations the API uses, by method.
var containerOps = map[string]string{
	"GET json": "", "GET logs": "", "GET stats": "", "HEAD archive": "", "GET archive": "", "PUT archive": "",
	"POST start": "", "POST stop": "", "POST kill": "", "POST wait": "", "POST pause": "", "POST unpause": "",
	"POST exec": "", "DELETE ": "",
}

func (g *Guard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		writeError(w, http.StatusForbidden, denied("path %q is not allowed", path))
		return
	}
	if loc := versionRe.FindStringIndex(path); loc != nil {
		path = "/" + path[loc[1]:]
	}
	if err := g.route(w, r, path); err != nil {
		status := http.StatusForbidden
		var se statusErr
		switch {
		case errors.As(err, &se):
			status = se.status
		case errdefs.IsNotFound(err):
			status = http.StatusNotFound
		case errdefs.IsConflict(err):
			status = http.StatusConflict
		case errdefs.IsInvalidParameter(err):
			status = http.StatusBadRequest
		case !strings.HasPrefix(err.Error(), "vaultrun docker guard:"):
			status = http.StatusInternalServerError
		}
		if status == http.StatusForbidden {
			slog.Warn("docker guard: refused", "method", r.Method, "path", r.URL.Path, "reason", err)
		}
		writeError(w, status, err)
	}
}

type statusErr struct {
	status int
	err    error
}

func (e statusErr) Error() string { return e.err.Error() }
func (e statusErr) Unwrap() error { return e.err }

func (g *Guard) route(w http.ResponseWriter, r *http.Request, path string) error {
	ctx := r.Context()
	m := r.Method
	switch {
	case path == ProbePath && m == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"guard": true, "runtime": g.Policy.Runtime})
		return nil
	case path == "/_ping" && (m == http.MethodGet || m == http.MethodHead),
		(path == "/version" || path == "/info" || path == "/images/json") && m == http.MethodGet,
		imageRe.MatchString(path) && m == http.MethodGet:
		g.proxy.ServeHTTP(w, r)
		return nil
	case path == "/images/create" && m == http.MethodPost:
		q := r.URL.Query()
		if q.Get("fromImage") == "" || q.Get("fromSrc") != "" {
			return denied("only pulling an image is allowed")
		}
		g.proxy.ServeHTTP(w, r)
		return nil
	case path == "/containers/create" && m == http.MethodPost:
		return g.createContainer(w, r)
	case path == "/networks/create" && m == http.MethodPost:
		return g.createNetwork(w, r)
	case path == "/volumes/create" && m == http.MethodPost:
		return g.createVolume(w, r)
	case path == "/vaultrun/workspaces" && m == http.MethodPost && g.Dirs != nil:
		return g.Dirs.handleCreate(w, r)
	case workspaceRe.MatchString(path) && m == http.MethodDelete && g.Dirs != nil:
		return g.Dirs.handleRemove(w, workspaceRe.FindStringSubmatch(path)[1])
	case path == "/vaultrun/xfs-quota" && m == http.MethodPost && g.Dirs != nil:
		return g.Dirs.handleQuota(w, r)
	}
	if sm := containerRe.FindStringSubmatch(path); sm != nil && sm[1] != "create" {
		if _, ok := containerOps[m+" "+sm[2]]; !ok {
			return denied("%s %s is not allowed", m, path)
		}
		name, managed, err := g.containerKind(ctx, sm[1])
		if err != nil {
			return err
		}
		isNetpolicy := g.Policy.Netpolicy != "" && name == "/"+g.Policy.Netpolicy
		if sm[2] == "exec" && (managed || isNetpolicy) {
			return g.createExec(w, r, sm[1], isNetpolicy)
		}
		if !managed {
			return denied("container %s is not a VaultRun sandbox", sm[1])
		}
		if sm[2] == "start" {
			if err := g.checkStart(ctx, sm[1]); err != nil {
				return err
			}
		}
		g.proxy.ServeHTTP(w, r)
		return nil
	}
	if sm := execRe.FindStringSubmatch(path); sm != nil {
		if (sm[2] == "json") != (m == http.MethodGet) || !g.knownExec(sm[1]) {
			return denied("exec %s was not created through the guard", sm[1])
		}
		g.proxy.ServeHTTP(w, r)
		return nil
	}
	if sm := networkRe.FindStringSubmatch(path); sm != nil && (m == http.MethodGet || m == http.MethodDelete) {
		nw, err := g.up.NetworkInspect(ctx, sm[1], network.InspectOptions{})
		if err != nil {
			return err
		}
		if nw.Labels[labelManaged] != "true" {
			return denied("network %s is not VaultRun's", sm[1])
		}
		g.proxy.ServeHTTP(w, r)
		return nil
	}
	return denied("%s %s is not allowed", m, path)
}

// containerKind returns a container's name and whether it is a VaultRun
// container.
func (g *Guard) containerKind(ctx context.Context, id string) (string, bool, error) {
	info, err := g.up.ContainerInspect(ctx, id)
	if err != nil {
		return "", false, err
	}
	return info.Name, info.Config != nil && info.Config.Labels[labelManaged] == "true", nil
}

// checkStart re-checks the bind mounts of a container about to start: Docker
// resolves them now, not at create.
func (g *Guard) checkStart(ctx context.Context, id string) error {
	info, err := g.up.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	if info.HostConfig == nil {
		return denied("container %s has no configuration", id)
	}
	if len(info.HostConfig.Binds) > 0 {
		return denied("container %s has Binds", id)
	}
	for _, m := range info.HostConfig.Mounts {
		if err := g.Policy.checkMount(m); err != nil {
			return err
		}
	}
	return nil
}

func (g *Guard) createContainer(w http.ResponseWriter, r *http.Request) error {
	var req container.CreateRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := g.Policy.CheckCreate(req.Config, req.HostConfig, req.NetworkingConfig); err != nil {
		return err
	}
	// The request is re-encoded from what was checked, so fields the guard
	// does not know never reach the daemon.
	resp, err := g.up.ContainerCreate(r.Context(), req.Config, req.HostConfig, req.NetworkingConfig, nil, r.URL.Query().Get("name"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (g *Guard) createExec(w http.ResponseWriter, r *http.Request, id string, netpolicy bool) error {
	var opts container.ExecOptions
	if err := decode(r, &opts); err != nil {
		return err
	}
	if err := g.Policy.CheckExec(&opts, netpolicy); err != nil {
		return err
	}
	resp, err := g.up.ContainerExecCreate(r.Context(), id, opts)
	if err != nil {
		return err
	}
	g.mu.Lock()
	now := time.Now()
	for k, t := range g.execs {
		if now.Sub(t) > 48*time.Hour {
			delete(g.execs, k)
		}
	}
	g.execs[resp.ID] = now
	g.mu.Unlock()
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (g *Guard) knownExec(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.execs[id]
	return ok
}

func (g *Guard) createNetwork(w http.ResponseWriter, r *http.Request) error {
	var req network.CreateRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := CheckNetworkCreate(&req); err != nil {
		return err
	}
	resp, err := g.up.NetworkCreate(r.Context(), req.Name, network.CreateOptions{Driver: "bridge", Labels: req.Labels, EnableIPv6: req.EnableIPv6, Internal: req.Internal})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (g *Guard) createVolume(w http.ResponseWriter, r *http.Request) error {
	var req volume.CreateOptions
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Name != toolsVolume || (req.Driver != "" && req.Driver != "local") || len(req.DriverOpts) > 0 || req.ClusterVolumeSpec != nil {
		return denied("only the %s volume may be created", toolsVolume)
	}
	v, err := g.up.VolumeCreate(r.Context(), volume.CreateOptions{Name: toolsVolume, Labels: req.Labels})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, v)
	return nil
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v); err != nil {
		return statusErr{http.StatusBadRequest, fmt.Errorf("vaultrun docker guard: invalid request body: %w", err)}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers the way the daemon does, so Docker clients show the
// message.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"message": err.Error()})
}
