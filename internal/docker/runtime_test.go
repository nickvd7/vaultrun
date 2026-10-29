package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/system"
)

func TestCheckRuntimeRegistered(t *testing.T) {
	if err := checkRuntimeRegistered("runsc", []string{"runc", "runsc"}); err != nil {
		t.Fatalf("registered runtime rejected: %v", err)
	}

	err := checkRuntimeRegistered("runsc", []string{"runc", "io.containerd.runc.v2"})
	if !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("want ErrRuntimeUnavailable, got %v", err)
	}
	// The error lists what the daemon does have, sorted, so the operator can
	// see a typo or a missing daemon.json entry at a glance.
	if !strings.Contains(err.Error(), `"runsc"`) || !strings.Contains(err.Error(), "io.containerd.runc.v2, runc") {
		t.Errorf("error does not name the runtime and the registered ones: %v", err)
	}

	// Matching is exact: a prefix or different case is not the same runtime.
	for _, want := range []string{"run", "RUNSC", "runsc "} {
		if err := checkRuntimeRegistered(want, []string{"runsc"}); err == nil {
			t.Errorf("%q matched registered runtime %q", want, "runsc")
		}
	}
	if err := checkRuntimeRegistered("runsc", nil); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Errorf("empty runtime list: want ErrRuntimeUnavailable, got %v", err)
	}
}

func TestNewReadsDockerRuntime(t *testing.T) {
	t.Setenv("DOCKER_RUNTIME", "  runsc ")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Runtime(); got != "runsc" {
		t.Errorf("Runtime() = %q, want %q", got, "runsc")
	}

	t.Setenv("DOCKER_RUNTIME", "")
	c, err = New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Runtime(); got != "" {
		t.Errorf("Runtime() = %q, want daemon default (empty)", got)
	}
	// Unset runtime needs no daemon: CheckRuntime must not call it.
	if err := c.CheckRuntime(t.Context()); err != nil {
		t.Errorf("CheckRuntime with no runtime configured: %v", err)
	}
}

func TestRuntimeNilClient(t *testing.T) {
	var c *Client
	if got := c.Runtime(); got != "" {
		t.Errorf("nil client Runtime() = %q, want empty", got)
	}
}

func TestGPUSupport(t *testing.T) {
	cases := []struct {
		name    string
		runtime string
		rt      system.Runtime
		wantErr bool
	}{
		{"runc", "runc", system.Runtime{Path: "runc"}, false},
		{"other runtime", "kata", system.Runtime{Path: "/usr/bin/kata-runtime"}, false},
		{"runsc without nvproxy", "runsc", system.Runtime{Path: "/usr/local/bin/runsc"}, true},
		{"runsc with other flags", "runsc", system.Runtime{Path: "/usr/local/bin/runsc", Args: []string{"--network=host"}}, true},
		{"runsc --nvproxy", "runsc", system.Runtime{Path: "/usr/local/bin/runsc", Args: []string{"--nvproxy"}}, false},
		{"runsc --nvproxy=true", "runsc", system.Runtime{Path: "/usr/local/bin/runsc", Args: []string{"--nvproxy=true"}}, false},
		{"runsc -nvproxy=true", "runsc", system.Runtime{Path: "/usr/local/bin/runsc", Args: []string{"-nvproxy=true"}}, false},
		{"runsc --nvproxy=false", "runsc", system.Runtime{Path: "/usr/local/bin/runsc", Args: []string{"--nvproxy=false"}}, true},
		// gVisor registered under another name is still recognised by its binary.
		{"renamed gVisor", "gvisor", system.Runtime{Path: "/usr/local/bin/runsc"}, true},
		// A containerd shim keeps its flags in runsc.toml; the daemon cannot tell.
		{"runsc shim", "runsc", system.Runtime{Type: "io.containerd.runsc.v1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := gpuSupport(tc.runtime, tc.rt)
			if tc.wantErr {
				if !errors.Is(err, ErrGPUUnsupported) {
					t.Fatalf("want ErrGPUUnsupported, got %v", err)
				}
				if !strings.Contains(err.Error(), "--runtime="+tc.runtime+" -- --nvproxy") {
					t.Errorf("error does not say how to fix it: %v", err)
				}
			} else if err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}

	var nilClient *Client
	if err := nilClient.GPUSupported(); err != nil {
		t.Errorf("nil client GPUSupported() = %v, want nil", err)
	}
}
