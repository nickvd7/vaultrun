package localgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAutomatedSmoke is the CI-friendly substitute for a manual Ollama smoke:
// mock upstream emits write_file + run_command tool_calls, gateway executes them
// against mock VaultRun, returns a final answer, captures a mission, and serves SSE.
func TestAutomatedSmoke(t *testing.T) {
	cfg := testConfig()
	vr := newMockVR()
	mm := &mockMissions{}
	up := &mockUpstream{
		responses: []*ChatResponse{
			{
				Choices: []ChatChoice{{
					Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{
							{
								ID:   "c1",
								Type: "function",
								Function: FunctionCall{
									Name:      toolWriteFile,
									Arguments: `{"path":"hi.txt","content":"hello"}`,
								},
							},
							{
								ID:   "c2",
								Type: "function",
								Function: FunctionCall{
									Name:      toolRunCommand,
									Arguments: `{"command":"cat","args":["hi.txt"]}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				}},
			},
			{
				ID:      "chatcmpl-smoke",
				Model:   "llama",
				Created: 1,
				Choices: []ChatChoice{{
					Message:      ChatMessage{Role: "assistant", Content: "Wrote hi.txt and cat worked."},
					FinishReason: "stop",
				}},
			},
		},
	}
	g := New(cfg, vr, up).WithMissions(mm)
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	// Non-stream path
	payload := map[string]any{
		"model":  "llama",
		"stream": false,
		"messages": []map[string]string{
			{"role": "user", "content": "Create hi.txt with hello and cat it"},
		},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerConversationID, "smoke-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if resp.Header.Get(headerSessionID) == "" {
		t.Fatal("missing session header")
	}
	if vr.runs != 1 {
		t.Fatalf("expected 1 run, got %d", vr.runs)
	}
	if len(mm.created) != 1 || len(mm.created[0].Steps) != 2 {
		t.Fatalf("mission capture incomplete: %+v", mm.created)
	}

	// Stream path (final answer only)
	up.responses = []*ChatResponse{{
		ID: "chatcmpl-s", Model: "llama", Created: 2,
		Choices: []ChatChoice{{
			Message: ChatMessage{Role: "assistant", Content: "streamed ok"}, FinishReason: "stop",
		}},
	}}
	payload["stream"] = true
	b, _ = json.Marshal(payload)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "streamed ok") || !strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("sse failed: %d %s", resp.StatusCode, raw)
	}
}
