package policy

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/open-policy-agent/opa/v1/rego"
)

// OPAHook evaluates commands and file access against an embedded Rego policy.
// The module must live in package vaultrun and define:
//
//	allow_command (boolean) — whether the command may run
//	allow_file    (boolean) — whether the file path may be accessed
//	deny_reason   (string, optional) — human-readable denial message
type OPAHook struct {
	commandQuery rego.PreparedEvalQuery
	fileQuery    rego.PreparedEvalQuery
	reasonQuery  rego.PreparedEvalQuery
}

// NewOPAHook compiles a Rego module string into an OPAHook ready for evaluation.
func NewOPAHook(ctx context.Context, module string) (*OPAHook, error) {
	cmdQ, err := rego.New(
		rego.Query("data.vaultrun.allow_command"),
		rego.Module("policy.rego", module),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile allow_command query: %w", err)
	}

	fileQ, err := rego.New(
		rego.Query("data.vaultrun.allow_file"),
		rego.Module("policy.rego", module),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile allow_file query: %w", err)
	}

	reasonQ, err := rego.New(
		rego.Query("data.vaultrun.deny_reason"),
		rego.Module("policy.rego", module),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile deny_reason query: %w", err)
	}

	return &OPAHook{commandQuery: cmdQ, fileQuery: fileQ, reasonQuery: reasonQ}, nil
}

// NewOPAHookFromFile reads a Rego policy file and compiles it.
func NewOPAHookFromFile(ctx context.Context, path string) (*OPAHook, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file %q: %w", path, err)
	}
	return NewOPAHook(ctx, string(b))
}

func (h *OPAHook) EvalCommand(ctx context.Context, sessionID uuid.UUID, command string, args []string) Decision {
	input := map[string]interface{}{
		"session_id": sessionID.String(),
		"command":    command,
		"args":       args,
	}
	return h.evalBool(ctx, h.commandQuery, input)
}

func (h *OPAHook) EvalFileAccess(ctx context.Context, sessionID uuid.UUID, path string, write bool) Decision {
	input := map[string]interface{}{
		"session_id": sessionID.String(),
		"path":       path,
		"write":      write,
	}
	return h.evalBool(ctx, h.fileQuery, input)
}

func (h *OPAHook) evalBool(ctx context.Context, query rego.PreparedEvalQuery, input map[string]interface{}) Decision {
	results, err := query.Eval(ctx, rego.EvalInput(input))
	if err != nil || len(results) == 0 || len(results[0].Expressions) == 0 {
		return Decision{Allowed: false, Reason: "policy evaluation failed"}
	}

	allowed, ok := results[0].Expressions[0].Value.(bool)
	if !ok {
		// Undefined result (rule not matched) — treat as deny
		return Decision{Allowed: false, Reason: "denied by policy"}
	}
	if allowed {
		return Decision{Allowed: true}
	}

	// Fetch optional human-readable reason
	reason := "denied by policy"
	rr, err := h.reasonQuery.Eval(ctx, rego.EvalInput(input))
	if err == nil && len(rr) > 0 && len(rr[0].Expressions) > 0 {
		if s, ok := rr[0].Expressions[0].Value.(string); ok && s != "" {
			reason = s
		}
	}
	return Decision{Allowed: false, Reason: reason}
}
