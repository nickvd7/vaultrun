package localgateway

import (
	"context"
	"strings"
	"testing"
)

func TestRunChatLoopExecutesToolThenFinishes(t *testing.T) {
	vr := newMockVR()
	up := &mockUpstream{
		responses: []*ChatResponse{
			{
				Choices: []ChatChoice{{
					Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{{
							ID:   "call_1",
							Type: "function",
							Function: FunctionCall{
								Name:      toolRunCommand,
								Arguments: `{"command":"echo","args":["hi"]}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "all good"},
					FinishReason: "stop",
				}},
			},
		},
	}
	g := New(testConfig(), vr, up)
	res, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model:    "llama",
		Messages: []ChatMessage{{Role: "user", Content: "run echo"}},
	}, "conv-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID == "" {
		t.Fatal("expected session id")
	}
	content, _ := res.Response.Choices[0].Message.Content.(string)
	if content != "all good" {
		t.Fatalf("got content %v", res.Response.Choices[0].Message.Content)
	}
	if vr.runs != 1 {
		t.Fatalf("expected 1 run, got %d", vr.runs)
	}
	// Upstream should have received injected tools.
	if len(up.calls) < 1 || len(up.calls[0].Tools) < 5 {
		t.Fatalf("expected injected tools, got %+v", up.calls)
	}
}

func TestRunChatLoopAllowsStreamFlag(t *testing.T) {
	g := New(testConfig(), newMockVR(), &mockUpstream{})
	res, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model:    "llama",
		Stream:   true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stream {
		t.Fatal("expected Stream=true on result")
	}
}

func TestRunChatLoopCapsIterations(t *testing.T) {
	cfg := testConfig()
	cfg.MaxToolLoops = 2
	up := &mockUpstream{}
	// Always return a tool call.
	up.responses = []*ChatResponse{
		toolCallResp(), toolCallResp(), toolCallResp(),
	}
	g := New(cfg, newMockVR(), up)
	_, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model:    "llama",
		Messages: []ChatMessage{{Role: "user", Content: "loop"}},
	}, "", "")
	if err == nil || !strings.Contains(err.Error(), "max tool loop") {
		t.Fatalf("expected loop limit, got %v", err)
	}
}

func toolCallResp() *ChatResponse {
	return &ChatResponse{
		Choices: []ChatChoice{{
			Message: ChatMessage{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "c",
					Type: "function",
					Function: FunctionCall{
						Name:      toolSessionInfo,
						Arguments: `{}`,
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
}
