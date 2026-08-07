// MCP verify_checkpoint — evaluate post-run assertions (exit/stdout/file).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

func verifyToolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name: "verify_checkpoint",
			Description: "Evaluate a post-run verification checkpoint: exit_code_zero, stdout_contains, " +
				"and/or file_exists. Optionally load observation from a VaultRun run_id via the API " +
				"(POST /api/v1/verify). Use after run_command or mission steps.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"run_id": {
						Type:        "string",
						Description: "Optional run ID — loads exit_code/stdout from VaultRun.",
					},
					"session_id": {
						Type:        "string",
						Description: "Session ID (required for file_exists when run_id is omitted).",
					},
					"exit_code_zero": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "When true, require exit_code == 0.",
					},
					"stdout_contains": {
						Type:        "string",
						Description: "Require this substring in stdout.",
					},
					"file_exists": {
						Type:        "string",
						Description: "Require this workspace path to exist (needs session_id or run_id).",
					},
					"exit_code": {
						Type:        "string",
						Description: "Inline exit code when not using run_id.",
					},
					"stdout": {
						Type:        "string",
						Description: "Inline stdout when not using run_id.",
					},
					"step_name": {
						Type:        "string",
						Description: "Optional label stored with the verification record.",
					},
					"persist": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "Persist result (default true).",
					},
				},
			},
		},
	}
}

func (s *server) toolVerifyCheckpoint(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	body := map[string]any{
		"spec": map[string]any{},
	}
	spec := body["spec"].(map[string]any)

	if v := args["exit_code_zero"]; v != "" {
		b, err := parseStrictToolBool(v)
		if err != nil {
			return mcpToolResult{}, fmt.Errorf("exit_code_zero: %w", err)
		}
		spec["exit_code_zero"] = b
	}
	if v := args["stdout_contains"]; v != "" {
		spec["stdout_contains"] = v
	}
	if v := args["file_exists"]; v != "" {
		spec["file_exists"] = v
	}
	if len(spec) == 0 {
		return mcpToolResult{}, fmt.Errorf("provide at least one of exit_code_zero, stdout_contains, file_exists")
	}

	if v := args["run_id"]; v != "" {
		body["run_id"] = v
	}
	if v := args["session_id"]; v != "" {
		body["session_id"] = v
	}
	if v := args["step_name"]; v != "" {
		body["step_name"] = v
	}
	if v := args["persist"]; v != "" {
		b, err := parseStrictToolBool(v)
		if err != nil {
			return mcpToolResult{}, fmt.Errorf("persist: %w", err)
		}
		body["persist"] = b
	}

	obs := map[string]any{}
	if v := args["exit_code"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return mcpToolResult{}, fmt.Errorf("exit_code: %w", err)
		}
		obs["exit_code"] = n
	}
	if v := args["stdout"]; v != "" {
		obs["stdout"] = v
	}
	if len(obs) > 0 {
		body["observation"] = obs
	}

	var result map[string]any
	if err := s.client.doJSON(ctx, "POST", "/api/v1/verify", body, &result); err != nil {
		return mcpToolResult{}, err
	}
	raw, _ := json.MarshalIndent(result, "", "  ")
	passed, _ := result["passed"].(bool)
	status := "FAILED"
	if passed {
		status = "PASSED"
	}
	return textResult(fmt.Sprintf("verify_checkpoint: %s\n%s", status, string(raw))), nil
}

func parseStrictToolBool(v string) (bool, error) {
	switch anyBoolString(v) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("must be true or false")
	}
}
