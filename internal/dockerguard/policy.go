package dockerguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
)

// Policy is what the API may ask of the Docker daemon. Everything it does not
// allow is refused.
type Policy struct {
	// WorkspaceDir is the workspace base directory as the Docker host sees
	// it. Bind mounts may only name a session directory directly in it.
	WorkspaceDir string
	// Runtime is the OCI runtime every container must use ("" = the daemon
	// default).
	Runtime string
	// Seccomp is the seccomp option sandboxes carry: the profile JSON,
	// "default", or "" for none.
	Seccomp string
	// Netpolicy names the container the API may run iptables in.
	Netpolicy string
}

const (
	labelManaged   = "vaultrun.managed"
	toolsVolume    = "vaultrun-tools"
	sessionNetPfx  = "vaultrun-net-"
	uuidPattern    = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	noNewPrivilege = "no-new-privileges"
)

var (
	// WorkspaceNameRe matches the directories the API may create, mount and
	// remove in the workspace base: sessions and warm pool entries.
	WorkspaceNameRe = regexp.MustCompile(`^(warm-)?` + uuidPattern + `$`)
	sessionNetRe    = regexp.MustCompile(`^` + sessionNetPfx + uuidPattern + `$`)
	chainRe         = regexp.MustCompile(`^vr-[0-9a-f]{1,12}(-in)?$`)
	bridgeRe        = regexp.MustCompile(`^br-[0-9a-f]{12}$`)
)

func denied(format string, args ...any) error {
	return fmt.Errorf("vaultrun docker guard: "+format, args...)
}

// CheckCreate decides whether a container may be created as requested.
func (p *Policy) CheckCreate(cfg *container.Config, hc *container.HostConfig, nc *network.NetworkingConfig) error {
	if cfg == nil || hc == nil {
		return denied("container create without Config or HostConfig")
	}
	if cfg.Labels[labelManaged] != "true" {
		return denied("containers must carry the label %s=true", labelManaged)
	}
	switch {
	case hc.Privileged:
		return denied("privileged containers are not allowed")
	case len(hc.CapAdd) > 0:
		return denied("adding capabilities is not allowed")
	case len(hc.Binds) > 0:
		return denied("Binds are not allowed; use Mounts")
	case len(hc.VolumesFrom) > 0, len(hc.Links) > 0:
		return denied("VolumesFrom and Links are not allowed")
	case len(hc.Devices) > 0, len(hc.DeviceCgroupRules) > 0:
		return denied("host devices are not allowed")
	case len(hc.PortBindings) > 0, hc.PublishAllPorts:
		return denied("publishing ports is not allowed")
	case len(hc.Sysctls) > 0:
		return denied("sysctls are not allowed")
	case hc.CgroupParent != "", hc.Cgroup != "":
		return denied("choosing a cgroup is not allowed")
	case hc.MaskedPaths != nil, hc.ReadonlyPaths != nil:
		return denied("changing masked or read-only paths is not allowed")
	case hc.PidMode != "", hc.IpcMode != "", hc.UTSMode != "", hc.UsernsMode != "":
		return denied("sharing a namespace is not allowed")
	case hc.CgroupnsMode != "" && hc.CgroupnsMode != "private":
		return denied("sharing the cgroup namespace is not allowed")
	case hc.Runtime != p.Runtime && !(p.Runtime == "" && hc.Runtime == "runc"):
		return denied("runtime %q is not allowed (want %q)", hc.Runtime, p.Runtime)
	}
	for _, dr := range hc.DeviceRequests {
		if dr.Driver != "nvidia" {
			return denied("device requests other than NVIDIA GPUs are not allowed")
		}
	}
	if err := p.checkSecurityOpt(hc.SecurityOpt); err != nil {
		return err
	}
	if err := checkNetworkName(string(hc.NetworkMode)); err != nil {
		return err
	}
	if nc != nil {
		for name := range nc.EndpointsConfig {
			if err := checkNetworkName(name); err != nil {
				return err
			}
		}
	}
	for _, m := range hc.Mounts {
		if err := p.checkMount(m); err != nil {
			return err
		}
	}
	return nil
}

func (p *Policy) checkSecurityOpt(opts []string) error {
	nnp := false
	for _, o := range opts {
		switch {
		case o == noNewPrivilege || o == noNewPrivilege+":true" || o == noNewPrivilege+"=true":
			nnp = true
		case strings.HasPrefix(o, "seccomp=") || strings.HasPrefix(o, "seccomp:"):
			if p.Seccomp == "" || o[len("seccomp="):] != p.Seccomp {
				return denied("seccomp profiles other than VaultRun's are not allowed")
			}
		default:
			return denied("security option %.40q is not allowed", o)
		}
	}
	if !nnp {
		return denied("containers must set no-new-privileges")
	}
	return nil
}

func checkNetworkName(name string) error {
	switch {
	case name == "", name == "none", name == "bridge", name == "default":
		return nil
	case sessionNetRe.MatchString(name):
		return nil
	}
	return denied("network %q is not allowed", name)
}

func (p *Policy) checkMount(m mount.Mount) error {
	switch m.Type {
	case mount.TypeTmpfs:
		return nil
	case mount.TypeVolume:
		if m.Source != toolsVolume || (m.VolumeOptions != nil && m.VolumeOptions.DriverConfig != nil) {
			return denied("volume %q is not allowed", m.Source)
		}
		return nil
	case mount.TypeBind:
		if m.BindOptions != nil {
			switch m.BindOptions.Propagation {
			case "", mount.PropagationPrivate, mount.PropagationRPrivate:
			default:
				return denied("bind propagation %q is not allowed", m.BindOptions.Propagation)
			}
		}
		_, err := p.WorkspacePath(m.Source)
		return err
	}
	return denied("mount type %q is not allowed", m.Type)
}

// WorkspacePath checks that source names a real directory directly in the
// workspace base (no symlink, no other path) and returns its name. The base
// directory is not writable by the API, so the directory cannot be swapped
// for a symlink after this check.
func (p *Policy) WorkspacePath(source string) (string, error) {
	if filepath.Clean(source) != source || filepath.Dir(source) != filepath.Clean(p.WorkspaceDir) {
		return "", denied("bind mount %q is outside the workspace directory", source)
	}
	name := filepath.Base(source)
	if !WorkspaceNameRe.MatchString(name) {
		return "", denied("bind mount %q is not a session workspace", source)
	}
	fi, err := os.Lstat(source)
	if err != nil {
		return "", denied("bind mount %q: %v", source, err)
	}
	if !fi.IsDir() {
		return "", denied("bind mount %q is not a directory", source)
	}
	return name, nil
}

// CheckExec decides whether a command may run in a container. netpolicy
// says whether the container is the netpolicy helper; other containers are
// managed sandboxes (the caller has checked the label).
func (p *Policy) CheckExec(opts *container.ExecOptions, netpolicy bool) error {
	if opts.Privileged {
		return denied("privileged exec is not allowed")
	}
	if !netpolicy {
		return nil
	}
	if opts.User != "" || len(opts.Env) > 0 || opts.WorkingDir != "" || opts.AttachStdin || opts.Tty {
		return denied("netpolicy: only plain iptables commands are allowed")
	}
	if len(opts.Cmd) == 0 || (opts.Cmd[0] != "iptables" && opts.Cmd[0] != "ip6tables") {
		return denied("netpolicy: only iptables and ip6tables may run")
	}
	return CheckIptables(opts.Cmd[1:])
}

// CheckIptables allows the rules VaultRun manages for sandbox egress (see
// internal/docker/netpolicy.go): its own vr-* chains, and the jumps to them
// (or a DROP) from FORWARD and INPUT for one sandbox bridge. Nothing else in
// the host firewall can be changed.
func CheckIptables(args []string) error {
	if len(args) < 2 {
		return denied("iptables: too few arguments")
	}
	for _, a := range args {
		if a == "-t" || strings.HasPrefix(a, "--table") || (strings.HasPrefix(a, "-t") && len(a) > 2 && !strings.HasPrefix(a, "--")) {
			return denied("iptables: other tables are not allowed")
		}
	}
	op, chain, rest := args[0], args[1], args[2:]
	switch op {
	case "-N", "-F", "-X":
		if !chainRe.MatchString(chain) || len(rest) > 0 {
			return denied("iptables %s: only VaultRun chains", op)
		}
		return nil
	case "-A":
		if !chainRe.MatchString(chain) {
			return denied("iptables -A: only VaultRun chains")
		}
		return nil
	case "-I", "-D":
		if chain != "FORWARD" && chain != "INPUT" {
			return denied("iptables %s: only FORWARD and INPUT", op)
		}
		if len(rest) != 4 || rest[0] != "-i" || !bridgeRe.MatchString(rest[1]) || rest[2] != "-j" ||
			(rest[3] != "DROP" && !chainRe.MatchString(rest[3])) {
			return denied("iptables %s %s: only a jump for one sandbox bridge", op, chain)
		}
		return nil
	}
	return denied("iptables %s is not allowed", op)
}

// CheckNetworkCreate allows the per-session bridge networks.
func CheckNetworkCreate(req *network.CreateRequest) error {
	switch {
	case !sessionNetRe.MatchString(req.Name):
		return denied("network name %q is not allowed", req.Name)
	case req.Driver != "" && req.Driver != "bridge":
		return denied("network driver %q is not allowed", req.Driver)
	case req.Labels[labelManaged] != "true":
		return denied("networks must carry the label %s=true", labelManaged)
	case len(req.Options) > 0, req.IPAM != nil && (len(req.IPAM.Config) > 0 || len(req.IPAM.Options) > 0 || req.IPAM.Driver != "" && req.IPAM.Driver != "default"),
		req.ConfigFrom != nil, req.ConfigOnly, req.Ingress, req.Scope != "" && req.Scope != "local":
		return denied("network options are not allowed")
	}
	return nil
}
