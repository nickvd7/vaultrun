// Jev tools — opt-in System One gate (TypeSafe or OpenJEV) over VaultRun evidence.
//
// Environment:
//
//	MCP_JEV_ENABLED     Set to "true" to expose jev_* tools
//	JEV_PROVIDER        typesafe | openjev (auto: OPENJEV_API_KEY alone → openjev)
//	OPENJEV_API_KEY     OpenJEV key (https://openjev.sh — api.openjev.sh)
//	TYPESAFE_API_KEY    TypeSafe key (api.typesafe.ai); also JEV_API_KEY
//	OPENJEV_BASE_URL / TYPESAFE_BASE_URL / JEV_BASE_URL — optional overrides
//	JEV_MODEL           Optional; default openjev or jev-latest by provider
//	JEV_ALLOW_PRIVATE_BASE  "true" to allow self-hosted OpenJev on loopback
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/nickvd7/vaultrun/internal/jev"
)

var errJevDisabled = errors.New("Jev is not enabled — set MCP_JEV_ENABLED=true and TYPESAFE_API_KEY")

func jevEnabled() bool {
	return os.Getenv("MCP_JEV_ENABLED") == "true"
}

func initJev(srv *server) {
	if !jevEnabled() {
		return
	}
	client, err := jev.ConfigFromEnv(os.Getenv)
	if err != nil {
		// Keep nil; tools return a clear error. Do not fail MCP startup.
		return
	}
	srv.jev = client
}

func (s *server) jevOrErr() (*jev.Client, error) {
	if s.jev == nil {
		if jevEnabled() {
			return nil, fmt.Errorf("Jev enabled but TYPESAFE_API_KEY missing or TYPESAFE_BASE_URL invalid")
		}
		return nil, errJevDisabled
	}
	return s.jev, nil
}

func jevToolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name: "jev_verify",
			Description: "Verify one or more claims against evidence using TypeSafe Jev (opt-in). " +
				"Prefer a VaultRun verify_evidence JSON blob as evidence. Returns per-claim " +
				"supports/contradicts/says_nothing judgments.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"evidence": {
						Type:        "string",
						Description: "Evidence text or verify_evidence JSON.",
					},
					"claim": {
						Type:        "string",
						Description: "Single claim (or use claims).",
					},
					"claims": {
						Type:        "string",
						Description: "JSON array of claim strings, or newline-separated claims.",
					},
					"verification_id": {
						Type:        "string",
						Description: "Optional: load sealed evidence from GET /verifications/:id/evidence.",
					},
					"include_controls": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "When using verification_id, embed control suite in evidence.",
					},
				},
			},
		},
		{
			Name: "jev_gate",
			Description: "Gate an agent completion claim against VaultRun evidence via Jev. " +
				"Returns action pass|fail|hold based on min_noul thresholds. " +
				"Use after verify_checkpoint / verify_evidence.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"claim": {
						Type:        "string",
						Description: "Completion claim to gate (required).",
					},
					"evidence": {
						Type:        "string",
						Description: "Evidence JSON/text (or use verification_id).",
					},
					"verification_id": {
						Type:        "string",
						Description: "Load sealed evidence from VaultRun.",
					},
					"task": {
						Type:        "string",
						Description: "Optional task description.",
					},
					"min_noul": {
						Type:        "string",
						Description: "Minimum noul for complete + evidence_backed (default 0.7).",
					},
					"on_fail": {
						Type:        "string",
						Enum:        []string{"fail", "hold"},
						Description: "Action when under threshold (default fail).",
					},
					"include_controls": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "Embed controls when loading verification_id.",
					},
					"mission_id": {
						Type:        "string",
						Description: "Optional: load Jev thresholds from mission step verify config.",
					},
					"step_index": {
						Type:        "string",
						Description: "0-based step index when mission_id is set.",
					},
				},
				Required: []string{"claim"},
			},
		},
	}
}

func (s *server) toolJevVerify(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.jevOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	evidence, err := s.resolveJevEvidence(ctx, args)
	if err != nil {
		return mcpToolResult{}, err
	}
	claims, err := parseJevClaims(args)
	if err != nil {
		return mcpToolResult{}, err
	}
	resp, err := client.VerifyClaims(ctx, evidence, claims)
	if err != nil {
		return mcpToolResult{}, err
	}
	raw, _ := json.MarshalIndent(resp, "", "  ")
	return textResult("jev_verify:\n" + string(raw)), nil
}

func (s *server) toolJevGate(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.jevOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}
	claim := strings.TrimSpace(args["claim"])
	if claim == "" {
		return mcpToolResult{}, fmt.Errorf("claim is required")
	}

	in := jev.GateInput{
		Claim:  claim,
		Task:   args["task"],
		OnFail: jev.ParseOnFail(args["on_fail"]),
	}
	if v := args["min_noul"]; v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 0 || n > 1 {
			return mcpToolResult{}, fmt.Errorf("min_noul must be between 0 and 1")
		}
		in.MinNoul = n
	}

	// Optional mission step config overlay.
	if mid := strings.TrimSpace(args["mission_id"]); mid != "" {
		idx := 0
		if v := args["step_index"]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return mcpToolResult{}, fmt.Errorf("step_index must be a non-negative integer")
			}
			idx = n
		}
		cfg, err := s.loadMissionStepJev(ctx, mid, idx)
		if err != nil {
			return mcpToolResult{}, err
		}
		if cfg.Claim != "" && claim == "" {
			in.Claim = cfg.Claim
		}
		if in.MinNoul == 0 && cfg.MinNoul > 0 {
			in.MinNoul = cfg.MinNoul
		}
		if args["on_fail"] == "" && cfg.OnFail != "" {
			in.OnFail = jev.ParseOnFail(cfg.OnFail)
		}
		if in.Task == "" {
			in.Task = cfg.Task
		}
	}

	evidence, err := s.resolveJevEvidence(ctx, args)
	if err != nil {
		return mcpToolResult{}, err
	}
	in.Evidence = evidence

	res, err := client.GateClaim(ctx, in)
	if err != nil {
		return mcpToolResult{}, err
	}
	raw, _ := json.MarshalIndent(res, "", "  ")
	return textResult(fmt.Sprintf("jev_gate: %s\n%s", res.Action, string(raw))), nil
}

func (s *server) resolveJevEvidence(ctx context.Context, args map[string]string) (string, error) {
	if v := strings.TrimSpace(args["evidence"]); v != "" {
		return v, nil
	}
	vid := strings.TrimSpace(args["verification_id"])
	if vid == "" {
		return "", fmt.Errorf("evidence or verification_id is required")
	}
	path := "/api/v1/verifications/" + vid + "/evidence"
	if args["include_controls"] == "true" || args["include_controls"] == "1" {
		path += "?include_controls=true"
	}
	var result map[string]any
	if err := s.client.doJSON(ctx, "GET", path, nil, &result); err != nil {
		return "", err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseJevClaims(args map[string]string) ([]string, error) {
	if v := strings.TrimSpace(args["claims"]); v != "" {
		if strings.HasPrefix(v, "[") {
			var arr []string
			if err := json.Unmarshal([]byte(v), &arr); err != nil {
				return nil, fmt.Errorf("claims: %w", err)
			}
			return arr, nil
		}
		parts := strings.Split(v, "\n")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("claims empty")
		}
		return out, nil
	}
	if v := strings.TrimSpace(args["claim"]); v != "" {
		return []string{v}, nil
	}
	return nil, fmt.Errorf("claim or claims is required")
}

type missionStepJev struct {
	Claim   string
	MinNoul float64
	OnFail  string
	Task    string
}

func (s *server) loadMissionStepJev(ctx context.Context, missionID string, stepIndex int) (missionStepJev, error) {
	var result map[string]any
	if err := s.client.doJSON(ctx, "GET", "/api/v1/missions/"+missionID, nil, &result); err != nil {
		return missionStepJev{}, err
	}
	// Response may be the mission object or wrapped.
	mission := result
	if m, ok := result["mission"].(map[string]any); ok {
		mission = m
	}
	steps, _ := mission["steps"].([]any)
	if stepIndex >= len(steps) {
		return missionStepJev{}, fmt.Errorf("step_index %d out of range (%d steps)", stepIndex, len(steps))
	}
	step, _ := steps[stepIndex].(map[string]any)
	verify, _ := step["verify"].(map[string]any)
	if verify == nil {
		return missionStepJev{}, nil
	}
	out := missionStepJev{
		Claim:  stringFromAny(verify["jev_claim"]),
		OnFail: stringFromAny(verify["jev_on_fail"]),
		Task:   stringFromAny(step["description"]),
	}
	if v, ok := verify["jev_min_noul"].(float64); ok {
		out.MinNoul = v
	}
	return out, nil
}

func stringFromAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}
