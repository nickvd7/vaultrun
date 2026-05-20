package runner

import (
	"context"
	"testing"

	"github.com/google/uuid"

	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/policy"
)

// resolveStatus is a pure function — test all branches without any I/O.

func TestResolveStatusCompleted(t *testing.T) {
	r := &dockerpkg.ExecResult{ExitCode: 0}
	if s := resolveStatus(nil, r); s != "completed" {
		t.Fatalf("want completed, got %s", s)
	}
}

func TestResolveStatusFailedOnExecError(t *testing.T) {
	r := &dockerpkg.ExecResult{}
	if s := resolveStatus(context.DeadlineExceeded, r); s != "failed" {
		t.Fatalf("want failed, got %s", s)
	}
}

func TestResolveStatusFailedOnNonZeroExit(t *testing.T) {
	r := &dockerpkg.ExecResult{ExitCode: 1}
	if s := resolveStatus(nil, r); s != "failed" {
		t.Fatalf("want failed, got %s", s)
	}
}

func TestResolveStatusTimeout(t *testing.T) {
	r := &dockerpkg.ExecResult{TimedOut: true}
	if s := resolveStatus(nil, r); s != "timeout" {
		t.Fatalf("want timeout, got %s", s)
	}
}

// prepareRun validates before touching the DB — test the early-return paths.

func newNoopRunner(hook policy.Hook) *Runner {
	return &Runner{hook: hook}
}

func TestPrepareRunEmptyCommand(t *testing.T) {
	r := newNoopRunner(policy.AllowAll{})
	_, err := r.prepareRun(context.Background(), RunRequest{Command: ""})
	if err == nil || err.Error() != "command is required" {
		t.Fatalf("expected 'command is required', got %v", err)
	}
}

func TestPrepareRunShellInjectionRejected(t *testing.T) {
	r := newNoopRunner(policy.AllowAll{})
	injections := []string{
		"cmd; rm -rf /",
		"cmd | cat /etc/passwd",
		"cmd && evil",
		"$(whoami)",
		"`id`",
		"cmd < /etc/passwd",
		"cmd > /tmp/out",
	}
	for _, cmd := range injections {
		_, err := r.prepareRun(context.Background(), RunRequest{Command: cmd})
		if err == nil {
			t.Errorf("expected rejection for command %q, got nil", cmd)
		}
	}
}

func TestPrepareRunPolicyDenial(t *testing.T) {
	r := newNoopRunner(policy.DenyAll{Reason: "test deny"})
	_, err := r.prepareRun(context.Background(), RunRequest{
		Command:   "python",
		SessionID: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected policy denial error")
	}
	if err.Error() != "command denied by policy: test deny" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRunValidCommandPassesValidation(t *testing.T) {
	// A valid command should pass the validation checks and only fail at
	// the DB step (nil db → panic, which we catch with recover).
	r := newNoopRunner(policy.AllowAll{})
	didPanic := func() (panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_, _ = r.prepareRun(context.Background(), RunRequest{
			Command:   "python",
			Args:      []string{"script.py"},
			SessionID: uuid.New(),
		})
		return false
	}()
	// We expect a panic from the nil DB, not an early-return error.
	if !didPanic {
		t.Fatal("expected nil DB to panic, meaning validation passed")
	}
}
