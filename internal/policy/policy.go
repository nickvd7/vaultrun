// Package policy provides a hook interface for future policy engines (OPA, etc.).
// In the MVP, hooks are no-ops. The interface is designed for extensibility.
package policy

import (
	"context"

	"github.com/google/uuid"
)

// Decision is the outcome of a policy evaluation.
type Decision struct {
	Allowed bool
	Reason  string
}

// Hook is the extension point for policy enforcement.
type Hook interface {
	// EvalCommand determines whether a command may execute in the given session.
	EvalCommand(ctx context.Context, sessionID uuid.UUID, command string, args []string) Decision
	// EvalFileAccess determines whether a file path may be accessed.
	EvalFileAccess(ctx context.Context, sessionID uuid.UUID, path string, write bool) Decision
}

// AllowAll is the default no-op implementation that permits everything.
type AllowAll struct{}

func (AllowAll) EvalCommand(_ context.Context, _ uuid.UUID, _ string, _ []string) Decision {
	return Decision{Allowed: true}
}

func (AllowAll) EvalFileAccess(_ context.Context, _ uuid.UUID, _ string, _ bool) Decision {
	return Decision{Allowed: true}
}

// DenyAll rejects every request with the given reason. Useful for tests.
type DenyAll struct{ Reason string }

func (d DenyAll) EvalCommand(_ context.Context, _ uuid.UUID, _ string, _ []string) Decision {
	return Decision{Allowed: false, Reason: d.Reason}
}

func (d DenyAll) EvalFileAccess(_ context.Context, _ uuid.UUID, _ string, _ bool) Decision {
	return Decision{Allowed: false, Reason: d.Reason}
}
