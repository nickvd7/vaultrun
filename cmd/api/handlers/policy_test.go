package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/policy"
)

// newPolicyTestContext builds a gin.Context/recorder pair for the given
// method/body, wired to a Hub configured with the given policy file path
// and policy hook.
func newPolicyTestContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

func newPolicyHub(policyFile string, hook policy.Hook) *Hub {
	return &Hub{
		cfg:    &config.Config{Auth: config.AuthConfig{OPAPolicyFile: policyFile}},
		policy: hook,
	}
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response body %q: %v", w.Body.String(), err)
	}
	return out
}

func TestPolicyGetDisabled(t *testing.T) {
	h := newPolicyHub("", nil)
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodGet, "/api/v1/policy", "")
	ph.Get(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)
	if got["enabled"] != false {
		t.Errorf("enabled = %v, want false", got["enabled"])
	}
	if _, ok := got["content"]; ok {
		t.Errorf("expected no content field when disabled, got %+v", got)
	}
}

func TestPolicyGetEnabledWithContent(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.rego")
	const policySrc = "package vaultrun\n\ndefault allow = true\n"
	if err := os.WriteFile(policyPath, []byte(policySrc), 0o600); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	h := newPolicyHub(policyPath, nil)
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodGet, "/api/v1/policy", "")
	ph.Get(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["content"] != policySrc {
		t.Errorf("content = %q, want %q", got["content"], policySrc)
	}
	if _, ok := got["error"]; ok {
		t.Errorf("expected no error field, got %+v", got)
	}
}

func TestPolicyGetUnreadableFileDoesNotLeakPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.rego")

	h := newPolicyHub(missing, nil)
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodGet, "/api/v1/policy", "")
	ph.Get(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)
	if got["enabled"] != true {
		t.Errorf("enabled = %v, want true", got["enabled"])
	}
	if got["error"] != "policy file unreadable" {
		t.Errorf("error = %q, want generic message", got["error"])
	}
	if strings.Contains(w.Body.String(), dir) {
		t.Errorf("response leaks host path: %s", w.Body.String())
	}
	if _, ok := got["content"]; ok {
		t.Errorf("expected no content field on read error, got %+v", got)
	}
}

func TestPolicyEvalInvalidJSON(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", "{not json")
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPolicyEvalMissingType(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"command":"ls"}`)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPolicyEvalInvalidType(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"network","command":"ls"}`)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	got := decodeJSON(t, w)
	if !strings.Contains(got["error"].(string), `"command" or "file"`) {
		t.Errorf("error = %q, want hint about valid types", got["error"])
	}
}

func TestPolicyEvalCommandTooLong(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	long := strings.Repeat("a", 4097)
	body := `{"type":"command","command":"` + long + `"}`
	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", body)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPolicyEvalPathTooLong(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	long := strings.Repeat("a", 4097)
	body := `{"type":"file","path":"/` + long + `"}`
	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", body)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPolicyEvalInvalidSessionID(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"command","command":"ls","session_id":"not-a-uuid"}`)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	got := decodeJSON(t, w)
	if got["error"] != "invalid session_id" {
		t.Errorf("error = %q, want %q", got["error"], "invalid session_id")
	}
}

func TestPolicyEvalCommandRequiredForCommandType(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"command"}`)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	got := decodeJSON(t, w)
	if got["error"] != "command is required for type=command" {
		t.Errorf("error = %q", got["error"])
	}
}

func TestPolicyEvalPathRequiredForFileType(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"file"}`)
	ph.Eval(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	got := decodeJSON(t, w)
	if got["error"] != "path is required for type=file" {
		t.Errorf("error = %q", got["error"])
	}
}

func TestPolicyEvalCommandAllowed(t *testing.T) {
	h := newPolicyHub("", policy.AllowAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval",
		`{"type":"command","command":"ls","args":["-la"],"session_id":"`+uuid.New().String()+`"}`)
	ph.Eval(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)
	if got["allowed"] != true {
		t.Errorf("allowed = %v, want true", got["allowed"])
	}
	if got["type"] != "command" {
		t.Errorf("type = %v, want command", got["type"])
	}
	if _, ok := got["reason"]; ok {
		t.Errorf("expected no reason when allowed, got %+v", got)
	}
}

func TestPolicyEvalCommandDeniedIncludesReason(t *testing.T) {
	h := newPolicyHub("", policy.DenyAll{Reason: "sandbox is locked down"})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"command","command":"rm -rf /"}`)
	ph.Eval(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)
	if got["allowed"] != false {
		t.Errorf("allowed = %v, want false", got["allowed"])
	}
	if got["reason"] != "sandbox is locked down" {
		t.Errorf("reason = %q, want %q", got["reason"], "sandbox is locked down")
	}
}

func TestPolicyEvalFileAccessUsesHookAndOmitsEmptyReason(t *testing.T) {
	h := newPolicyHub("", policy.DenyAll{})
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"file","path":"/etc/passwd","write":true}`)
	ph.Eval(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeJSON(t, w)
	if got["allowed"] != false {
		t.Errorf("allowed = %v, want false", got["allowed"])
	}
	if got["type"] != "file" {
		t.Errorf("type = %v, want file", got["type"])
	}
	if _, ok := got["reason"]; ok {
		t.Errorf("expected no reason field for empty-reason denial, got %+v", got)
	}
}

func TestPolicyEvalCancelledContextPropagatesToHook(t *testing.T) {
	hook := &recordingHook{}
	h := newPolicyHub("", hook)
	ph := NewPolicyHandler(h)

	c, w := newPolicyTestContext(http.MethodPost, "/api/v1/policy/eval", `{"type":"command","command":"echo hi"}`)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)

	ph.Eval(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if hook.lastCtx == nil || hook.lastCtx.Err() == nil {
		t.Error("expected the request context (cancelled) to be forwarded to the policy hook")
	}
}

// recordingHook captures the context passed to EvalCommand/EvalFileAccess so
// tests can assert that the request context (and its cancellation) propagates.
type recordingHook struct {
	lastCtx context.Context
}

func (r *recordingHook) EvalCommand(ctx context.Context, _ uuid.UUID, _ string, _ []string) policy.Decision {
	r.lastCtx = ctx
	return policy.Decision{Allowed: true}
}

func (r *recordingHook) EvalFileAccess(ctx context.Context, _ uuid.UUID, _ string, _ bool) policy.Decision {
	r.lastCtx = ctx
	return policy.Decision{Allowed: true}
}
