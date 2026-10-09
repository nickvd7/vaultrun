package localgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// streamChunk is an OpenAI-compatible chat.completion.chunk.
type streamChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []streamChunkChoice `json:"choices"`
}

type streamChunkChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// writeSSEFinal streams the final assistant message as OpenAI SSE chunks.
// Tool rounds always run non-streaming upstream; only the final answer is streamed.
func writeSSEFinal(w http.ResponseWriter, resp *ChatResponse) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported by response writer")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	content := messageContentString(resp)
	id := resp.ID
	model := resp.Model
	created := resp.Created

	// Role chunk first.
	if err := writeSSEData(w, streamChunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []streamChunkChoice{{Index: 0, Delta: streamDelta{Role: "assistant"}}},
	}); err != nil {
		return err
	}
	flusher.Flush()

	for _, part := range splitUTF8Chunks(content, 48) {
		if err := writeSSEData(w, streamChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []streamChunkChoice{{Index: 0, Delta: streamDelta{Content: part}}},
		}); err != nil {
			return err
		}
		flusher.Flush()
	}

	stop := "stop"
	if err := writeSSEData(w, streamChunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []streamChunkChoice{{Index: 0, Delta: streamDelta{}, FinishReason: &stop}},
	}); err != nil {
		return err
	}
	flusher.Flush()

	_, err := fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
	return err
}

func writeSSEData(w http.ResponseWriter, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err
}

func messageContentString(resp *ChatResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	switch c := resp.Choices[0].Message.Content.(type) {
	case string:
		return c
	case nil:
		return ""
	default:
		b, _ := json.Marshal(c)
		return string(b)
	}
}

func splitUTF8Chunks(s string, maxRunes int) []string {
	if s == "" {
		return nil
	}
	if maxRunes < 1 {
		maxRunes = 1
	}
	var out []string
	var b strings.Builder
	n := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		b.WriteRune(r)
		n++
		if n >= maxRunes {
			out = append(out, b.String())
			b.Reset()
			n = 0
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
