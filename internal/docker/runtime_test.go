package docker

import (
	"errors"
	"strings"
	"testing"
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
