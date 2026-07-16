// Flowd tools for the MCP server — local workflow automation via flowctl.
//
// Environment variables:
//
//	MCP_FLOWD_ENABLED  Set to "true" to expose flowd_* tools (explicit opt-in)
//	FLOWCTL_PATH       Path to flowctl binary (default: flowctl)
//	FLOWD_CONFIG       Optional config file path passed as --config to flowctl
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const flowdExecTimeout = 30 * time.Second

type flowdConfig struct {
	flowctlPath string
	configPath  string
}

func flowdEnabled() bool {
	return os.Getenv("MCP_FLOWD_ENABLED") == "true"
}

func initFlowd(srv *server) {
	if !flowdEnabled() {
		return
	}
	srv.flowd = &flowdConfig{
		flowctlPath: getEnvOrDefault("FLOWCTL_PATH", "flowctl"),
		configPath:  os.Getenv("FLOWD_CONFIG"),
	}
}

var errFlowdDisabled = errors.New("Flowd is not enabled — set MCP_FLOWD_ENABLED=true and install flowctl " +
	"(cargo install --path crates/flow-cli from https://github.com/nickvd7/flowd)")

func (s *server) flowdOrErr() (*flowdConfig, error) {
	if s.flowd == nil {
		return nil, errFlowdDisabled
	}
	return s.flowd, nil
}

func (s *server) execFlowctl(ctx context.Context, subcommand ...string) (string, error) {
	cfg, err := s.flowdOrErr()
	if err != nil {
		return "", err
	}

	args := make([]string, 0, len(subcommand)+2)
	if cfg.configPath != "" {
		args = append(args, "--config", cfg.configPath)
	}
	args = append(args, subcommand...)

	execCtx, cancel := context.WithTimeout(ctx, flowdExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, cfg.flowctlPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				msg = "flowctl timed out after 30s"
			} else if errors.Is(err, exec.ErrNotFound) {
				msg = fmt.Sprintf("flowctl not found at %q — install from https://github.com/nickvd7/flowd", cfg.flowctlPath)
			} else {
				msg = err.Error()
			}
		}
		return "", fmt.Errorf("flowctl %s: %s", strings.Join(subcommand, " "), msg)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		out = strings.TrimSpace(stderr.String())
	}
	if out == "" {
		out = "(no output)"
	}
	return out, nil
}

func flowdToolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name:        "flowd_list_suggestions",
			Description: "List pending workflow automation suggestions from the local Flowd daemon. " +
				"Requires flowctl and flow-daemon running on the same machine as vaultrun-mcp.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name: "flowd_explain_suggestion",
			Description: "Show explainable reasoning for Flowd suggestions. " +
				"Pass suggestion_id to filter to one suggestion when supported by flowctl.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"suggestion_id": {
						Type:        "string",
						Description: "Optional suggestion ID to explain.",
					},
				},
			},
		},
		{
			Name:        "flowd_approve_suggestion",
			Description: "Approve a Flowd suggestion by ID, turning it into an automation. Nothing runs until you explicitly execute it.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"suggestion_id": {Type: "string", Description: "Suggestion ID from flowd_list_suggestions."},
				},
				Required: []string{"suggestion_id"},
			},
		},
		{
			Name:        "flowd_list_patterns",
			Description: "List detected workflow patterns in the local Flowd database.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "flowd_stats",
			Description: "Show local Flowd usage statistics (patterns, suggestions, runs).",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "flowd_undo_run",
			Description: "Undo a Flowd automation run by run_id. Reverses file changes when undo is supported.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"run_id": {Type: "string", Description: "Run ID from a previous Flowd execution."},
				},
				Required: []string{"run_id"},
			},
		},
	}
}

func (s *server) toolFlowdListSuggestions(ctx context.Context) (mcpToolResult, error) {
	out, err := s.execFlowctl(ctx, "suggestions")
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return textResult(out), nil
}

func (s *server) toolFlowdExplainSuggestion(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sub := []string{"suggestions", "--explain"}
	if id := args["suggestion_id"]; id != "" {
		sub = append(sub, id)
	}
	out, err := s.execFlowctl(ctx, sub...)
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return textResult(out), nil
}

func (s *server) toolFlowdApproveSuggestion(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	id := args["suggestion_id"]
	if id == "" {
		return mcpToolResult{}, fmt.Errorf("suggestion_id is required")
	}
	out, err := s.execFlowctl(ctx, "approve", id)
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return textResult(out), nil
}

func (s *server) toolFlowdListPatterns(ctx context.Context) (mcpToolResult, error) {
	out, err := s.execFlowctl(ctx, "patterns")
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return textResult(out), nil
}

func (s *server) toolFlowdStats(ctx context.Context) (mcpToolResult, error) {
	out, err := s.execFlowctl(ctx, "stats")
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return textResult(out), nil
}

func (s *server) toolFlowdUndoRun(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	id := args["run_id"]
	if id == "" {
		return mcpToolResult{}, fmt.Errorf("run_id is required")
	}
	out, err := s.execFlowctl(ctx, "undo", id)
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return textResult(out), nil
}

func textResult(out string) mcpToolResult {
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: out}}}
}
