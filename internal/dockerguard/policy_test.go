package dockerguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
)

const testSession = "0b9e3c1e-2f4d-4c6a-9e1b-7a5d3c2b1a00"

func testPolicy(t *testing.T) (*Policy, string) {
	t.Helper()
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, testSession), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Policy{WorkspaceDir: base, Runtime: "runsc", Seccomp: `{"defaultAction":"SCMP_ACT_ERRNO"}`, Netpolicy: "vaultrun-netpolicy"}, base
}

// sandbox is what CreateSandbox sends (internal/docker/container.go).
func sandbox(p *Policy, base string) (*container.Config, *container.HostConfig, *network.NetworkingConfig) {
	cfg := &container.Config{
		Image:  "python:3.12-slim",
		Cmd:    strslice.StrSlice{"sleep", "infinity"},
		User:   "65532:65532",
		Labels: map[string]string{"vaultrun.managed": "true", "vaultrun.session": testSession},
	}
	hc := &container.HostConfig{
		NetworkMode: container.NetworkMode("vaultrun-net-" + testSession),
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: filepath.Join(base, testSession), Target: "/workspace",
				BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate}},
			{Type: mount.TypeTmpfs, Target: "/tmp"},
			{Type: mount.TypeVolume, Source: "vaultrun-tools", Target: "/.vaultrun", ReadOnly: true},
		},
		ReadonlyRootfs: true,
		SecurityOpt:    []string{"no-new-privileges", "seccomp=" + p.Seccomp},
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{},
		Runtime:        p.Runtime,
		Resources: container.Resources{DeviceRequests: []container.DeviceRequest{
			{Driver: "nvidia", Capabilities: [][]string{{"gpu"}}, Count: -1},
		}},
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{"vaultrun-net-" + testSession: {}}}
	return cfg, hc, nc
}

func TestCheckCreateAllowsSandboxes(t *testing.T) {
	p, base := testPolicy(t)
	cfg, hc, nc := sandbox(p, base)
	if err := p.CheckCreate(cfg, hc, nc); err != nil {
		t.Fatalf("sandbox refused: %v", err)
	}
	for _, mode := range []string{"none", "bridge"} {
		hc.NetworkMode = container.NetworkMode(mode)
		if err := p.CheckCreate(cfg, hc, nil); err != nil {
			t.Fatalf("network %s refused: %v", mode, err)
		}
	}
}

func TestCheckCreateRefusesHostAccess(t *testing.T) {
	p, base := testPolicy(t)
	if err := os.Symlink("/", filepath.Join(base, "1b9e3c1e-2f4d-4c6a-9e1b-7a5d3c2b1a00")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "2b9e3c1e-2f4d-4c6a-9e1b-7a5d3c2b1a00"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bind := func(src string) func(*container.Config, *container.HostConfig, *network.NetworkingConfig) {
		return func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Mounts[0].Source = src
		}
	}
	cases := map[string]func(*container.Config, *container.HostConfig, *network.NetworkingConfig){
		"no managed label": func(c *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig) {
			delete(c.Labels, "vaultrun.managed")
		},
		"privileged": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) { hc.Privileged = true },
		"cap add": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.CapAdd = []string{"SYS_ADMIN"}
		},
		"binds": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Binds = []string{"/:/host"}
		},
		"bind root":       bind("/"),
		"bind base":       bind(base),
		"bind outside":    bind("/etc"),
		"bind traversal":  bind(base + "/../etc"),
		"bind nested":     bind(filepath.Join(base, testSession, "x")),
		"bind symlink":    bind(filepath.Join(base, "1b9e3c1e-2f4d-4c6a-9e1b-7a5d3c2b1a00")),
		"bind file":       bind(filepath.Join(base, "2b9e3c1e-2f4d-4c6a-9e1b-7a5d3c2b1a00")),
		"bind other name": bind(filepath.Join(base, "snapshots")),
		"bind shared": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Mounts[0].BindOptions.Propagation = mount.PropagationRShared
		},
		"other volume": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Mounts[2].Source = "postgres_data"
		},
		"volume driver": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Mounts[2].VolumeOptions = &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: "local"}}
		},
		"npipe": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Mounts[1].Type = mount.TypeNamedPipe
		},
		"volumes from": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.VolumesFrom = []string{"vaultrun-postgres-1"}
		},
		"host network": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.NetworkMode = "host"
		},
		"container network": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.NetworkMode = "container:vaultrun-netpolicy"
		},
		"other network": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.NetworkMode = "deployments_default"
		},
		"other endpoint": func(_ *container.Config, _ *container.HostConfig, nc *network.NetworkingConfig) {
			nc.EndpointsConfig["deployments_default"] = nil
		},
		"host pid": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) { hc.PidMode = "host" },
		"host ipc": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) { hc.IpcMode = "host" },
		"host uts": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) { hc.UTSMode = "host" },
		"host userns": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.UsernsMode = "host"
		},
		"host cgroupns": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.CgroupnsMode = "host"
		},
		"cgroup parent": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.CgroupParent = "/"
		},
		"device": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Devices = []container.DeviceMapping{{PathOnHost: "/dev/sda"}}
		},
		"device rule": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.DeviceCgroupRules = []string{"a *:* rwm"}
		},
		"other device request": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.DeviceRequests[0].Driver = "cdi"
		},
		"ports": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.PublishAllPorts = true
		},
		"sysctl": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.Sysctls = map[string]string{"kernel.x": "1"}
		},
		"unmasked paths": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.MaskedPaths = []string{}
		},
		"other runtime": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) { hc.Runtime = "runc" },
		"seccomp unconfined": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.SecurityOpt[1] = "seccomp=unconfined"
		},
		"other seccomp": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.SecurityOpt[1] = `seccomp={"defaultAction":"SCMP_ACT_ALLOW"}`
		},
		"apparmor unconfined": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.SecurityOpt = append(hc.SecurityOpt, "apparmor=unconfined")
		},
		"label disable": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.SecurityOpt = append(hc.SecurityOpt, "label=disable")
		},
		"no no-new-privileges": func(_ *container.Config, hc *container.HostConfig, _ *network.NetworkingConfig) {
			hc.SecurityOpt = hc.SecurityOpt[1:]
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, hc, nc := sandbox(p, base)
			mutate(cfg, hc, nc)
			err := p.CheckCreate(cfg, hc, nc)
			if err == nil {
				t.Fatal("allowed")
			}
			if !strings.HasPrefix(err.Error(), "vaultrun docker guard:") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckCreateRuntimeDefault(t *testing.T) {
	p, base := testPolicy(t)
	p.Runtime = ""
	cfg, hc, nc := sandbox(p, base)
	for _, rt := range []string{"", "runc"} {
		hc.Runtime = rt
		if err := p.CheckCreate(cfg, hc, nc); err != nil {
			t.Fatalf("runtime %q refused: %v", rt, err)
		}
	}
	hc.Runtime = "kata"
	if err := p.CheckCreate(cfg, hc, nc); err == nil {
		t.Fatal("runtime kata allowed")
	}
}

// TestCheckIptables accepts every rule internal/docker/netpolicy.go installs
// or removes, and nothing else.
func TestCheckIptables(t *testing.T) {
	chain, in, iface := "vr-0123456789ab", "vr-0123456789ab-in", "br-0123456789ab"
	allowed := [][]string{
		{"-N", chain}, {"-F", chain}, {"-X", chain}, {"-N", in}, {"-F", in}, {"-X", in},
		{"-A", chain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{"-A", chain, "-p", "udp", "--dport", "53", "-d", "172.19.0.1", "-j", "ACCEPT"},
		{"-A", chain, "-d", "10.0.0.0/8", "-j", "DROP"},
		{"-A", chain, "-j", "DROP"},
		{"-A", in, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
		{"-I", "FORWARD", "-i", iface, "-j", chain}, {"-D", "FORWARD", "-i", iface, "-j", chain},
		{"-I", "INPUT", "-i", iface, "-j", in}, {"-D", "INPUT", "-i", iface, "-j", in},
		{"-I", "FORWARD", "-i", iface, "-j", "DROP"},
	}
	for _, a := range allowed {
		if err := CheckIptables(a); err != nil {
			t.Errorf("%v refused: %v", a, err)
		}
	}
	refused := [][]string{
		{"-F"}, {"-F", "FORWARD"}, {"-F", "INPUT"}, {"-X", "DOCKER"}, {"-P", "INPUT", "ACCEPT"},
		{"-A", "INPUT", "-j", "ACCEPT"}, {"-A", "DOCKER-USER", "-j", "ACCEPT"},
		{"-I", "INPUT", "-j", "ACCEPT"}, {"-I", "INPUT", "1", "-i", iface, "-j", in},
		{"-I", "FORWARD", "-i", "eth0", "-j", "ACCEPT"}, {"-I", "FORWARD", "-i", iface, "-j", "ACCEPT"},
		{"-D", "OUTPUT", "-i", iface, "-j", chain},
		{"-t", "nat", "-A", chain, "-j", "DROP"}, {"-A", chain, "-t", "nat", "-j", "DROP"}, {"-A", chain, "--table=nat"},
		{"-N", "vr-x;rm"}, {"-Z"}, {"-S"},
	}
	for _, a := range refused {
		if err := CheckIptables(a); err == nil {
			t.Errorf("%v allowed", a)
		}
	}
}

func TestCheckExec(t *testing.T) {
	p, _ := testPolicy(t)
	ok := &container.ExecOptions{Cmd: []string{"iptables", "-N", "vr-0123456789ab"}}
	if err := p.CheckExec(ok, true); err != nil {
		t.Fatal(err)
	}
	for name, o := range map[string]*container.ExecOptions{
		"shell":      {Cmd: []string{"sh", "-c", "iptables -F"}},
		"env":        {Cmd: ok.Cmd, Env: []string{"XTABLES_LIBDIR=/tmp"}},
		"user":       {Cmd: ok.Cmd, User: "0"},
		"privileged": {Cmd: ok.Cmd, Privileged: true},
		"bad rule":   {Cmd: []string{"iptables", "-F", "INPUT"}},
	} {
		if err := p.CheckExec(o, true); err == nil {
			t.Errorf("netpolicy %s allowed", name)
		}
	}
	if err := p.CheckExec(&container.ExecOptions{Cmd: []string{"sh"}, User: "1000"}, false); err != nil {
		t.Errorf("sandbox exec refused: %v", err)
	}
	if err := p.CheckExec(&container.ExecOptions{Cmd: []string{"sh"}, Privileged: true}, false); err == nil {
		t.Error("privileged sandbox exec allowed")
	}
}

func TestCheckNetworkCreate(t *testing.T) {
	good := network.CreateRequest{Name: "vaultrun-net-" + testSession, CreateOptions: network.CreateOptions{
		Driver: "bridge", Labels: map[string]string{"vaultrun.managed": "true"}}}
	if err := CheckNetworkCreate(&good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*network.CreateRequest){
		"name":   func(r *network.CreateRequest) { r.Name = "evil" },
		"driver": func(r *network.CreateRequest) { r.Driver = "macvlan" },
		"label":  func(r *network.CreateRequest) { r.Labels = nil },
		"options": func(r *network.CreateRequest) {
			r.Options = map[string]string{"com.docker.network.bridge.name": "eth0"}
		},
		"subnet": func(r *network.CreateRequest) {
			r.IPAM = &network.IPAM{Config: []network.IPAMConfig{{Subnet: "192.168.1.0/24"}}}
		},
		"ingress": func(r *network.CreateRequest) { r.Ingress = true },
		"swarm":   func(r *network.CreateRequest) { r.Scope = "swarm" },
	} {
		r := good
		r.Labels = map[string]string{"vaultrun.managed": "true"}
		mutate(&r)
		if err := CheckNetworkCreate(&r); err == nil {
			t.Errorf("%s allowed", name)
		}
	}
}
