package localgateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

type mockVR struct {
	mu       sync.Mutex
	sessions map[string]*vaultrun.Session
	files    map[string]map[string][]byte // session → path → content
	runs     int
	failRun  error
}

func newMockVR() *mockVR {
	return &mockVR{
		sessions: make(map[string]*vaultrun.Session),
		files:    make(map[string]map[string][]byte),
	}
}

func (m *mockVR) CreateSession(_ context.Context, opts vaultrun.CreateSessionOptions) (*vaultrun.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("11111111-1111-1111-1111-%012d", len(m.sessions)+1)
	img := opts.Image
	if img == "" {
		img = "python:3.12-slim"
	}
	s := &vaultrun.Session{
		ID:             id,
		Image:          img,
		Status:         "running",
		NetworkEnabled: opts.NetworkEnabled,
	}
	m.sessions[id] = s
	m.files[id] = make(map[string][]byte)
	return s, nil
}

func (m *mockVR) GetSession(_ context.Context, sessionID string) (*vaultrun.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	cp := *s
	return &cp, nil
}

func (m *mockVR) DeleteSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	delete(m.files, sessionID)
	return nil
}

func (m *mockVR) Run(_ context.Context, sessionID string, opts vaultrun.RunOptions) (*vaultrun.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failRun != nil {
		return nil, m.failRun
	}
	if _, ok := m.sessions[sessionID]; !ok {
		return nil, fmt.Errorf("session not found")
	}
	m.runs++
	stdout := fmt.Sprintf("ran %s %s", opts.Command, strings.Join(opts.Args, " "))
	code := 0
	id := fmt.Sprintf("run-%d", m.runs)
	return &vaultrun.Run{
		ID:        id,
		SessionID: sessionID,
		Command:   opts.Command,
		Args:      opts.Args,
		Status:    "completed",
		ExitCode:  &code,
		Stdout:    &stdout,
	}, nil
}

func (m *mockVR) UploadFile(_ context.Context, sessionID, remotePath string, content io.Reader) (*vaultrun.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sessionID]; !ok {
		return nil, fmt.Errorf("session not found")
	}
	b, err := io.ReadAll(content)
	if err != nil {
		return nil, err
	}
	m.files[sessionID][remotePath] = b
	return &vaultrun.File{Path: remotePath, SizeBytes: int64(len(b)), SessionID: sessionID}, nil
}

func (m *mockVR) DownloadFile(_ context.Context, sessionID, remotePath string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[sessionID][remotePath]
	if !ok {
		return nil, fmt.Errorf("file not found")
	}
	return io.NopCloser(bytes.NewReader(f)), nil
}

func (m *mockVR) ListFiles(_ context.Context, sessionID string) ([]*vaultrun.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*vaultrun.File, 0, len(m.files[sessionID]))
	for p, b := range m.files[sessionID] {
		out = append(out, &vaultrun.File{Path: p, SizeBytes: int64(len(b)), SessionID: sessionID})
	}
	return out, nil
}

type mockUpstream struct {
	mu        sync.Mutex
	responses []*ChatResponse
	calls     []ChatRequest
	err       error
	models    *ModelsResponse
}

func (u *mockUpstream) ChatCompletions(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) == 0 {
		return &ChatResponse{
			Choices: []ChatChoice{{
				Message:      ChatMessage{Role: "assistant", Content: "done"},
				FinishReason: "stop",
			}},
		}, nil
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func (u *mockUpstream) ListModels(_ context.Context) (*ModelsResponse, error) {
	if u.models != nil {
		return u.models, nil
	}
	return &ModelsResponse{Object: "list", Data: []ModelObject{{ID: "llama", Object: "model"}}}, nil
}

func testConfig() Config {
	return Config{
		ListenAddr:           ":0",
		AuthToken:            "test-token-16chars",
		UpstreamURL:          "http://127.0.0.1:11434",
		UpstreamTimeout:      5 * time.Second,
		VaultRunBaseURL:      "http://127.0.0.1:8080",
		VaultRunAPIKey:       "vr_test",
		DefaultImage:         "python:3.12-slim",
		MaxToolLoops:         4,
		MaxBodyBytes:         1 << 20,
		MaxToolResultBytes:   4096,
		MaxFileBytes:         8192,
		RateLimitPerMin:      1000,
		DefaultModel:         "llama",
		RunTimeoutSeconds:    30,
		MaxRunTimeoutSeconds: 60,
	}
}
