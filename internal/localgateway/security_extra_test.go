package localgateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

func reqWithAuth(authorization string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://example/", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	return r
}

func TestExplicitSessionIDMustBeUUID(t *testing.T) {
	g := New(testConfig(), newMockVR(), &mockUpstream{})
	_, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model:    "llama",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, "", "not-a-uuid")
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("expected UUID validation error, got %v", err)
	}
}

func TestWriteFileEnforcesSizeCap(t *testing.T) {
	cfg := testConfig()
	cfg.MaxFileBytes = 16
	vr := newMockVR()
	g := New(cfg, vr, &mockUpstream{})
	ctx := context.Background()
	sess, _ := vr.CreateSession(ctx, vaultrun.CreateSessionOptions{})
	_, err := g.executeTool(ctx, sess.ID, toolWriteFile,
		`{"path":"big.txt","content":"abcdefghijklmnopqrstuvwxyz"}`)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected size cap error, got %v", err)
	}
}

func TestClientToolNotExecuted(t *testing.T) {
	vr := newMockVR()
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
								Name:      "my_helper",
								Arguments: `{}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
			},
			{
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "done"},
					FinishReason: "stop",
				}},
			},
		},
	}
	g := New(testConfig(), vr, up)
	_, err := g.RunChatLoop(context.Background(), ChatRequest{
		Model: "llama",
		Messages: []ChatMessage{{Role: "user", Content: "x"}},
		Tools: []ToolDef{{
			Type:     "function",
			Function: ToolFunction{Name: "my_helper"},
		}},
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Second upstream call should have seen a tool error message, not a real side effect.
	if len(up.calls) < 2 {
		t.Fatal("expected second upstream round")
	}
	found := false
	for _, m := range up.calls[1].Messages {
		if m.Role == "tool" {
			s, _ := m.Content.(string)
			if strings.Contains(s, "not executed") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected non-execution notice in tool message, messages=%+v", up.calls[1].Messages)
	}
	if vr.runs != 0 {
		t.Fatalf("client tool must not trigger sandbox runs, got %d", vr.runs)
	}
}

func TestAuthRejectsPrefixAndEmptyBearer(t *testing.T) {
	cfg := testConfig()
	g := New(cfg, newMockVR(), &mockUpstream{})
	if g.authorize(reqWithAuth("Bearer " + cfg.AuthToken[:8])) {
		t.Fatal("prefix must not authenticate")
	}
	if g.authorize(reqWithAuth("Bearer ")) {
		t.Fatal("empty bearer must fail")
	}
	if g.authorize(reqWithAuth(cfg.AuthToken)) {
		t.Fatal("raw token without Bearer must fail")
	}
	if !g.authorize(reqWithAuth("Bearer " + cfg.AuthToken)) {
		t.Fatal("exact bearer must pass")
	}
}
