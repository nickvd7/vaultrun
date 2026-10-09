package localgateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// LoopResult is the final chat response plus the bound session id.
type LoopResult struct {
	Response  *ChatResponse
	SessionID string
	Stream    bool
}

// RunChatLoop proxies the request to the upstream model, executes VaultRun
// tools for each tool_calls round, and returns the final assistant message.
// Upstream tool rounds are always non-streaming; req.Stream only affects how
// the HTTP handler delivers the final answer (JSON vs SSE).
func (g *Gateway) RunChatLoop(ctx context.Context, req ChatRequest, conversationKey, explicitSessionID string) (*LoopResult, error) {
	if len(req.Messages) == 0 {
		return nil, &GatewayError{Status: 400, Code: "invalid_request", Message: "messages must be non-empty"}
	}
	if err := validateMessages(req.Messages); err != nil {
		return nil, &GatewayError{Status: 400, Code: "invalid_request", Message: err.Error()}
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = g.cfg.DefaultModel
	}
	if model == "" {
		return nil, &GatewayError{Status: 400, Code: "invalid_request", Message: "model is required (or set LOCAL_GATEWAY_DEFAULT_MODEL)"}
	}

	sessionID, err := g.ensureSession(ctx, conversationKey, explicitSessionID)
	if err != nil {
		return nil, &GatewayError{Status: 502, Code: "session_error", Message: err.Error()}
	}

	messages := append([]ChatMessage(nil), req.Messages...)
	tools := mergeTools(req.Tools)
	var captured []capturedStep

	for i := 0; i < g.cfg.MaxToolLoops; i++ {
		upReq := ChatRequest{
			Model:       model,
			Messages:    messages,
			Tools:       tools,
			Stream:      false, // always buffer upstream; stream only the final answer
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
			TopP:        req.TopP,
		}
		resp, err := g.upstream.ChatCompletions(ctx, upReq)
		if err != nil {
			return nil, &GatewayError{Status: 502, Code: "upstream_error", Message: err.Error()}
		}
		if len(resp.Choices) == 0 {
			return nil, &GatewayError{Status: 502, Code: "upstream_error", Message: "upstream returned no choices"}
		}
		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			if resp.ID == "" {
				resp.ID = "chatcmpl-" + uuid.NewString()
			}
			if resp.Object == "" {
				resp.Object = "chat.completion"
			}
			if resp.Created == 0 {
				resp.Created = time.Now().Unix()
			}
			if resp.Model == "" {
				resp.Model = model
			}
			g.captureMissionBestEffort(ctx, conversationKey, sessionID, captured)
			return &LoopResult{Response: resp, SessionID: sessionID, Stream: req.Stream}, nil
		}

		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: msg.ToolCalls,
		})

		for _, tc := range msg.ToolCalls {
			name := tc.Function.Name
			args := tc.Function.Arguments
			if args == "" {
				args = "{}"
			}
			callID := tc.ID
			if callID == "" {
				callID = "call_" + uuid.NewString()
			}

			var result string
			if !isVaultRunTool(name) {
				result = fmt.Sprintf(`{"error":"tool %q is not executed by VaultRun local gateway"}`, name)
			} else {
				out, execErr := g.executeTool(ctx, sessionID, name, args)
				if execErr != nil {
					slog.Warn("localgateway: tool failed",
						"tool", name, "session_id", sessionID, "err", execErr)
					result = fmt.Sprintf(`{"error":%q}`, execErr.Error())
				} else {
					result = out
					captured = append(captured, capturedStep{
						Name:     name,
						Tool:     name,
						ArgsJSON: args,
						ResultOK: true,
					})
				}
			}
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    result,
			})
		}
	}

	return nil, &GatewayError{
		Status:  429,
		Code:    "tool_loop_limit",
		Message: fmt.Sprintf("exceeded max tool loop iterations (%d)", g.cfg.MaxToolLoops),
	}
}

func validateMessages(msgs []ChatMessage) error {
	if len(msgs) > 200 {
		return fmt.Errorf("too many messages (max 200)")
	}
	for i, m := range msgs {
		switch m.Role {
		case "system", "user", "assistant", "tool":
		default:
			return fmt.Errorf("messages[%d]: invalid role %q", i, m.Role)
		}
		if m.Role == "tool" && m.ToolCallID == "" {
			return fmt.Errorf("messages[%d]: tool messages require tool_call_id", i)
		}
	}
	return nil
}

// GatewayError is an HTTP-mapped error.
type GatewayError struct {
	Status  int
	Code    string
	Message string
}

func (e *GatewayError) Error() string { return e.Message }
