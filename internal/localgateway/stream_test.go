package localgateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteSSEFinal(t *testing.T) {
	cfg := testConfig()
	vr := newMockVR()
	up := &mockUpstream{
		responses: []*ChatResponse{{
			ID:      "chatcmpl-test",
			Created: 1,
			Model:   "llama",
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "hello world from vault"},
				FinishReason: "stop",
			}},
		}},
	}
	g := New(cfg, vr, up)
	ts := httptest.NewServer(g.Handler())
	defer ts.Close()

	body := `{"model":"llama","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type %q", resp.Header.Get("Content-Type"))
	}
	raw, _ := ioReadAll(resp)
	if !strings.Contains(raw, "data: [DONE]") {
		t.Fatalf("missing DONE: %s", raw)
	}
	if !strings.Contains(raw, "hello") {
		t.Fatalf("missing content: %s", raw)
	}
	// Parse at least one chunk.
	sc := bufio.NewScanner(strings.NewReader(raw))
	foundChunk := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var ch streamChunk
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ch); err != nil {
			t.Fatal(err)
		}
		foundChunk = true
		break
	}
	if !foundChunk {
		t.Fatal("no SSE chunks parsed")
	}
}

func TestSplitUTF8Chunks(t *testing.T) {
	parts := splitUTF8Chunks("abcdef", 2)
	if len(parts) != 3 || parts[0] != "ab" || parts[2] != "ef" {
		t.Fatalf("got %v", parts)
	}
}

func ioReadAll(resp *http.Response) (string, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.String(), err
}
