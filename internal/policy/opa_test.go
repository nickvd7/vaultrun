package policy_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/policy"
)

const allowAllModule = `
package vaultrun
import rego.v1
default allow_command := true
default allow_file := true
`

const denyShellModule = `
package vaultrun
import rego.v1

blocked_commands := {"bash", "sh", "zsh", "curl", "wget", "nc"}

default allow_command := false
allow_command if { not input.command in blocked_commands }

deny_reason := r if {
    input.command in blocked_commands
    r := concat("", ["command blocked by policy: ", input.command])
}

default allow_file := false

allow_file if { not input.write }
`

func TestOPAAllowAllCommand(t *testing.T) {
	ctx := context.Background()
	h, err := policy.NewOPAHook(ctx, allowAllModule)
	if err != nil {
		t.Fatalf("NewOPAHook: %v", err)
	}
	d := h.EvalCommand(ctx, uuid.New(), "python", []string{"script.py"})
	if !d.Allowed {
		t.Errorf("expected allowed, got denied: %s", d.Reason)
	}
}

func TestOPAAllowAllFile(t *testing.T) {
	ctx := context.Background()
	h, err := policy.NewOPAHook(ctx, allowAllModule)
	if err != nil {
		t.Fatalf("NewOPAHook: %v", err)
	}
	d := h.EvalFileAccess(ctx, uuid.New(), "/workspace/data.csv", true)
	if !d.Allowed {
		t.Errorf("expected allowed, got denied: %s", d.Reason)
	}
}

func TestOPADenyBlockedCommand(t *testing.T) {
	ctx := context.Background()
	h, err := policy.NewOPAHook(ctx, denyShellModule)
	if err != nil {
		t.Fatalf("NewOPAHook: %v", err)
	}
	for _, cmd := range []string{"bash", "sh", "curl", "wget"} {
		d := h.EvalCommand(ctx, uuid.New(), cmd, nil)
		if d.Allowed {
			t.Errorf("command %q should be denied", cmd)
		}
		if d.Reason == "" {
			t.Errorf("denial of %q should include reason", cmd)
		}
	}
}

func TestOPAAllowSafeCommand(t *testing.T) {
	ctx := context.Background()
	h, err := policy.NewOPAHook(ctx, denyShellModule)
	if err != nil {
		t.Fatalf("NewOPAHook: %v", err)
	}
	for _, cmd := range []string{"python", "node", "go", "ls"} {
		d := h.EvalCommand(ctx, uuid.New(), cmd, []string{"--help"})
		if !d.Allowed {
			t.Errorf("command %q should be allowed, got: %s", cmd, d.Reason)
		}
	}
}

func TestOPADenyReason(t *testing.T) {
	ctx := context.Background()
	h, err := policy.NewOPAHook(ctx, denyShellModule)
	if err != nil {
		t.Fatalf("NewOPAHook: %v", err)
	}
	d := h.EvalCommand(ctx, uuid.New(), "bash", []string{"-c", "id"})
	if d.Allowed {
		t.Fatal("bash should be denied")
	}
	want := "command blocked by policy: bash"
	if d.Reason != want {
		t.Errorf("reason = %q, want %q", d.Reason, want)
	}
}

func TestOPADenyFileWrite(t *testing.T) {
	ctx := context.Background()
	h, err := policy.NewOPAHook(ctx, denyShellModule)
	if err != nil {
		t.Fatalf("NewOPAHook: %v", err)
	}
	// denyShellModule denies file writes
	d := h.EvalFileAccess(ctx, uuid.New(), "/workspace/out.txt", true)
	if d.Allowed {
		t.Error("file write should be denied by denyShellModule")
	}
	// reads are still allowed
	d2 := h.EvalFileAccess(ctx, uuid.New(), "/workspace/in.txt", false)
	if !d2.Allowed {
		t.Errorf("file read should be allowed, got: %s", d2.Reason)
	}
}

func TestOPAInvalidRegoRejected(t *testing.T) {
	ctx := context.Background()
	_, err := policy.NewOPAHook(ctx, "this is not valid rego at all")
	if err == nil {
		t.Error("expected error for invalid Rego module")
	}
}
