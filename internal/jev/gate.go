package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// OnFailAction controls mission/gateway behavior when the gate rejects.
type OnFailAction string

const (
	OnFailFail OnFailAction = "fail"
	OnFailHold OnFailAction = "hold"
)

// GateInput is a completion / claim check against evidence.
type GateInput struct {
	Claim      string
	Evidence   string // sealed verify evidence JSON or tool transcript
	Task       string // optional task description
	MinNoul    float64
	OnFail     OnFailAction
}

// GateResult is the structured gate outcome.
type GateResult struct {
	Action       string            `json:"action"` // pass | fail | hold
	Passed       bool              `json:"passed"`
	CompleteNoul float64           `json:"complete_noul"`
	EvidenceNoul float64           `json:"evidence_noul"`
	MinNoul      float64           `json:"min_noul"`
	Model        string            `json:"model,omitempty"`
	Summary      string            `json:"summary"`
	Answers      map[string]Answer `json:"answers,omitempty"`
}

// ParseOnFail normalizes on_fail values.
func ParseOnFail(v string) OnFailAction {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "hold", "review", "pause":
		return OnFailHold
	default:
		return OnFailFail
	}
}

// GateClaim asks Jev whether the claim is complete and evidence-backed.
func (c *Client) GateClaim(ctx context.Context, in GateInput) (*GateResult, error) {
	if err := ValidateClaim(in.Claim); err != nil {
		return nil, err
	}
	min := in.MinNoul
	if min <= 0 {
		min = DefaultMinNoul
	}
	if min > 1 {
		min = 1
	}
	onFail := in.OnFail
	if onFail == "" {
		onFail = OnFailFail
	}

	stateObj := map[string]any{
		"claim":    SanitizeState(in.Claim),
		"evidence": SanitizeState(in.Evidence),
	}
	if strings.TrimSpace(in.Task) != "" {
		stateObj["task"] = SanitizeState(in.Task)
	}
	// Prefer structured state; fallback string if marshal fails.
	var state any = stateObj
	if raw, err := json.Marshal(stateObj); err == nil {
		state = json.RawMessage(raw)
	}

	questions := map[string]Question{
		"complete": {
			Type: "noul",
			Instructions: "Given the task (if any) and the claim, does the claim accurately describe work that is finished and addresses the task?",
		},
		"evidence_backed": {
			Type: "noul",
			Instructions: "Does the evidence support the claim (tests/observations/verify results match what the claim asserts)? Answer no if evidence is missing, contradictory, or only the agent's self-report.",
		},
	}

	resp, err := c.Evaluate(ctx, state, questions)
	if err != nil {
		return nil, err
	}

	complete := resp.Answers["complete"].Noul
	evidence := resp.Answers["evidence_backed"].Noul
	ok := complete >= min && evidence >= min

	out := &GateResult{
		Passed:       ok,
		CompleteNoul: complete,
		EvidenceNoul: evidence,
		MinNoul:      min,
		Model:        resp.Model,
		Answers:      resp.Answers,
	}
	if ok {
		out.Action = "pass"
		out.Summary = fmt.Sprintf("jev gate passed (complete=%.2f evidence=%.2f min=%.2f)", complete, evidence, min)
	} else {
		out.Action = string(onFail)
		out.Summary = fmt.Sprintf("jev gate %s (complete=%.2f evidence=%.2f min=%.2f)", onFail, complete, evidence, min)
	}
	return out, nil
}

// VerifyClaims runs per-claim support checks (supports / contradicts / says_nothing).
func (c *Client) VerifyClaims(ctx context.Context, evidence string, claims []string) (*Response, error) {
	if len(claims) == 0 {
		return nil, fmt.Errorf("at least one claim is required")
	}
	if len(claims) > 20 {
		return nil, fmt.Errorf("too many claims (max 20)")
	}
	state := map[string]any{
		"evidence": SanitizeState(evidence),
		"claims":   claims,
	}
	questions := make(map[string]Question, len(claims))
	for i, claim := range claims {
		if err := ValidateClaim(claim); err != nil {
			return nil, fmt.Errorf("claims[%d]: %w", i, err)
		}
		key := fmt.Sprintf("claim_%d", i)
		questions[key] = Question{
			Type: "choice",
			Instructions: map[string]any{
				"claim":    SanitizeState(claim),
				"question": "Relative to `evidence`, does the evidence support, contradict, or say nothing about `claim`?",
			},
			Criteria: map[string]any{
				"supports":    "Evidence clearly supports the claim",
				"contradicts": "Evidence contradicts the claim",
				"says_nothing": "Evidence is silent or insufficient",
			},
		}
	}
	return c.Evaluate(ctx, state, questions)
}
