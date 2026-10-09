package localgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nickvd7/vaultrun/internal/jev"
)

// errJevContinue signals the chat loop should nudge the model and retry.
var errJevContinue = errors.New("jev gate: continue working")

func anyContentString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case nil:
		return ""
	default:
		b, _ := json.Marshal(c)
		return string(b)
	}
}

func (g *Gateway) maybeJevCompletionGate(ctx context.Context, claim any, messages []ChatMessage, steps []capturedStep, retries *int) error {
	if g == nil || !g.cfg.JevEnabled || g.jev == nil {
		return nil
	}
	claimStr := strings.TrimSpace(anyContentString(claim))
	if claimStr == "" {
		// Empty final answer — nothing to gate.
		return nil
	}
	evidence := buildJevEvidenceFromLoop(messages, steps)
	res, err := g.jev.GateClaim(ctx, jev.GateInput{
		Claim:    claimStr,
		Evidence: evidence,
		Task:     firstUserTask(messages),
		MinNoul:  g.cfg.JevMinNoul,
		OnFail:   jev.ParseOnFail(g.cfg.JevOnFail),
	})
	if err != nil {
		slog.Warn("localgateway: jev gate error", "err", err)
		// Fail closed when on_fail=fail; otherwise allow answer (hold soft).
		if jev.ParseOnFail(g.cfg.JevOnFail) == jev.OnFailFail {
			return &GatewayError{Status: 502, Code: "jev_error", Message: "completion gate unavailable"}
		}
		return nil
	}
	if res.Passed {
		return nil
	}
	slog.Info("localgateway: jev gate rejected", "action", res.Action, "complete", res.CompleteNoul, "evidence", res.EvidenceNoul)
	if res.Action == string(jev.OnFailHold) || jev.ParseOnFail(g.cfg.JevOnFail) == jev.OnFailHold {
		if retries != nil && *retries < g.cfg.JevMaxRetries {
			*retries++
			return errJevContinue
		}
		return &GatewayError{
			Status:  409,
			Code:    "jev_hold",
			Message: fmt.Sprintf("completion held by jev gate: %s", res.Summary),
		}
	}
	return &GatewayError{
		Status:  409,
		Code:    "jev_fail",
		Message: fmt.Sprintf("completion rejected by jev gate: %s", res.Summary),
	}
}

func buildJevEvidenceFromLoop(messages []ChatMessage, steps []capturedStep) string {
	type toolEv struct {
		Tool   string `json:"tool"`
		OK     bool   `json:"ok"`
		Args   string `json:"args,omitempty"`
		Result string `json:"result,omitempty"`
	}
	ev := struct {
		Tools    []toolEv `json:"tools"`
		ToolMsgs []string `json:"tool_messages,omitempty"`
	}{}
	for _, s := range steps {
		ev.Tools = append(ev.Tools, toolEv{Tool: s.Tool, OK: s.ResultOK, Args: truncate(s.ArgsJSON, 512)})
	}
	for _, m := range messages {
		if m.Role == "tool" {
			ev.ToolMsgs = append(ev.ToolMsgs, truncate(anyContentString(m.Content), 1024))
		}
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func firstUserTask(messages []ChatMessage) string {
	for _, m := range messages {
		if m.Role == "user" {
			s := strings.TrimSpace(anyContentString(m.Content))
			if s != "" {
				return truncate(s, 2000)
			}
		}
	}
	return ""
}
