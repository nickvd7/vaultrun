package localgateway

import (
	"context"
	"strings"
	"testing"
)

func TestMissionCaptureOnSuccessfulTools(t *testing.T) {
	vr := newMockVR()
	mm := &mockMissions{}
	up := &mockUpstream{
		responses: []*ChatResponse{
			{
				Choices: []ChatChoice{{
					Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{{
							ID:   "c1",
							Type: "function",
							Function: FunctionCall{
								Name:      toolWriteFile,
								Arguments: `{"path":"a.txt","content":"x"}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "wrote it"},
					FinishReason: "stop",
				}},
			},
		},
	}
	g := New(testConfig(), vr, up).WithMissions(mm)
	_, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model:    "llama",
		Messages: []ChatMessage{{Role: "user", Content: "write"}},
	}, "demo-conv", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(mm.created) != 1 {
		t.Fatalf("expected 1 mission, got %d", len(mm.created))
	}
	if mm.created[0].Slug == "" || !strings.HasPrefix(mm.created[0].Slug, "local-") {
		t.Fatalf("bad slug %q", mm.created[0].Slug)
	}
	if len(mm.runs) != 1 {
		t.Fatalf("expected mission run, got %v", mm.runs)
	}
}

func TestMissionCaptureDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.CaptureMissions = false
	mm := &mockMissions{}
	up := &mockUpstream{
		responses: []*ChatResponse{
			toolCallResp(),
			{Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}},
		},
	}
	// Even with missions attached, CaptureMissions=false skips.
	g := New(cfg, newMockVR(), up).WithMissions(mm)
	_, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model:    "llama",
		Messages: []ChatMessage{{Role: "user", Content: "x"}},
	}, "c", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(mm.created) != 0 {
		t.Fatalf("expected no capture, got %d", len(mm.created))
	}
}

func TestSlugify(t *testing.T) {
	if got := slugify("Hello World!"); got != "hello-world" {
		t.Fatalf("got %q", got)
	}
}
