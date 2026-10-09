package localgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nickvd7/vaultrun/internal/jev"
)

func TestMaybeJevGatePass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jev.Response{
			Answers: map[string]jev.Answer{
				"complete":        {Type: "noul", Noul: 0.95},
				"evidence_backed": {Type: "noul", Noul: 0.9},
			},
		})
	}))
	defer srv.Close()

	g := &Gateway{
		cfg: Config{JevEnabled: true, JevMinNoul: 0.7, JevOnFail: "fail"},
		jev: &jev.Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()},
	}
	retries := 0
	err := g.maybeJevCompletionGate(context.Background(), "done", nil, nil, &retries)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaybeJevGateHoldContinue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jev.Response{
			Answers: map[string]jev.Answer{
				"complete":        {Type: "noul", Noul: 0.1},
				"evidence_backed": {Type: "noul", Noul: 0.1},
			},
		})
	}))
	defer srv.Close()

	g := &Gateway{
		cfg: Config{JevEnabled: true, JevMinNoul: 0.8, JevOnFail: "hold", JevMaxRetries: 1},
		jev: &jev.Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()},
	}
	retries := 0
	err := g.maybeJevCompletionGate(context.Background(), "I finished", []ChatMessage{
		{Role: "user", Content: "fix the bug"},
	}, []capturedStep{{Tool: "run_command", ResultOK: true}}, &retries)
	if err != errJevContinue {
		t.Fatalf("want continue, got %v", err)
	}
	if retries != 1 {
		t.Fatalf("retries=%d", retries)
	}
	// Exhaust retries → 409 hold
	err = g.maybeJevCompletionGate(context.Background(), "I finished", nil, nil, &retries)
	ge, ok := err.(*GatewayError)
	if !ok || ge.Code != "jev_hold" {
		t.Fatalf("got %v", err)
	}
}

func TestMaybeJevGateDisabled(t *testing.T) {
	g := &Gateway{cfg: Config{JevEnabled: false}}
	if err := g.maybeJevCompletionGate(context.Background(), "x", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMaybeJevGateFailClosedWhenClientNil(t *testing.T) {
	g := &Gateway{cfg: Config{JevEnabled: true}, jev: nil}
	err := g.maybeJevCompletionGate(context.Background(), "done", nil, nil, nil)
	ge, ok := err.(*GatewayError)
	if !ok || ge.Code != "jev_unavailable" {
		t.Fatalf("got %v", err)
	}
}

func TestMaybeJevGateUpstreamErrorFailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`busy`))
	}))
	defer srv.Close()
	g := &Gateway{
		cfg: Config{JevEnabled: true, JevOnFail: "hold"}, // hold must NOT fail-open on infra errors
		jev: &jev.Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()},
	}
	err := g.maybeJevCompletionGate(context.Background(), "done", nil, nil, nil)
	ge, ok := err.(*GatewayError)
	if !ok || ge.Code != "jev_error" {
		t.Fatalf("got %v", err)
	}
}

func TestBuildJevEvidenceRedactsNothingLocally(t *testing.T) {
	raw := buildJevEvidenceFromLoop(
		[]ChatMessage{{Role: "tool", Content: "ok"}},
		[]capturedStep{{Tool: "run_command", ArgsJSON: `{"command":"echo"}`, ResultOK: true}},
	)
	if raw == "" || raw == "{}" {
		t.Fatal(raw)
	}
}
