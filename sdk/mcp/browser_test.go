package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/browser"
)

// browserCapture is a stub VaultRun API that records the run_command request
// body it receives (in particular, the generated Python script) and returns a
// canned success response.
type browserCapture struct {
	lastRun runCapturedRequest
}

type runCapturedRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func newBrowserTestServer(t *testing.T, cap *browserCapture) (*server, func()) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/run") {
			var req runCapturedRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode run request: %v", err)
			}
			cap.lastRun = req
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Run{
				ID:        "run-1",
				SessionID: "sess-1",
				Status:    "completed",
			})
			return
		}
		http.Error(w, "unexpected path", http.StatusNotFound)
	}))

	client := newVaultRunClient(ts.URL, "test-key")
	srv := newServer(client, "python:3.12-slim", "", fsConfig{})
	return srv, ts.Close
}

// scriptArg extracts the Python source passed as `python3 -c <script>` from a
// captured run_command request.
func scriptArg(t *testing.T, cap *browserCapture) string {
	t.Helper()
	if cap.lastRun.Command != "python3" {
		t.Fatalf("command = %q, want python3", cap.lastRun.Command)
	}
	if len(cap.lastRun.Args) != 2 || cap.lastRun.Args[0] != "-c" {
		t.Fatalf("args = %v, want [\"-c\", <script>]", cap.lastRun.Args)
	}
	return cap.lastRun.Args[1]
}

// assertFieldEscaped verifies that a caller-supplied field reached the
// generated script only in its Python-escaped form: the raw (dangerous) value
// must not appear verbatim, and the correctly escaped value must. This proves
// the handler routed the field through browser.EscapePythonString rather than
// interpolating it directly. (internal/browser's own tests already prove
// EscapePythonString itself is correct; this test proves the MCP tool calls it.)
func assertFieldEscaped(t *testing.T, label, script, raw string) {
	t.Helper()
	escaped := browser.EscapePythonString(raw)
	if escaped == raw {
		t.Fatalf("%s: test input %q needs no escaping — pick a value with a quote or backslash", label, raw)
	}
	if strings.Contains(script, raw) {
		t.Errorf("%s: raw unescaped value %q appears verbatim in generated script:\n%s", label, raw, script)
	}
	if !strings.Contains(script, escaped) {
		t.Errorf("%s: expected escaped value %q in generated script:\n%s", label, escaped, script)
	}
}

// ---------------------------------------------------------------------------
// browser_navigate
// ---------------------------------------------------------------------------

func TestBrowserNavigateRequiresArgs(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	if _, err := srv.toolBrowserNavigate(context.Background(), map[string]string{"url": "https://example.com"}); err == nil {
		t.Error("missing session_id: want error, got nil")
	}
	if _, err := srv.toolBrowserNavigate(context.Background(), map[string]string{"session_id": "s1"}); err == nil {
		t.Error("missing url: want error, got nil")
	}
}

// TestBrowserNavigateBlocksSSRFTargets verifies the same private-network and
// dangerous-scheme refusals as internal/browser apply to the MCP tool, since
// this is the code path actually reachable by an agent.
func TestBrowserNavigateBlocksSSRFTargets(t *testing.T) {
	blocked := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://127.0.0.1:6379/",
		"http://localhost/admin",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"gopher://127.0.0.1:6379/_INFO",
	}

	for _, target := range blocked {
		t.Run(target, func(t *testing.T) {
			srv, cleanup := newBrowserTestServer(t, &browserCapture{})
			defer cleanup()

			_, err := srv.toolBrowserNavigate(context.Background(), map[string]string{
				"session_id": "s1",
				"url":        target,
			})
			if err == nil {
				t.Errorf("navigate(%q) = nil error, want SSRF/scheme refusal", target)
			}
		})
	}
}

func TestBrowserNavigateAllowsPublicURL(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	if _, err := srv.toolBrowserNavigate(context.Background(), map[string]string{
		"session_id": "s1",
		"url":        "https://example.com/path?q=1",
	}); err != nil {
		t.Fatalf("navigate to public URL: %v", err)
	}

	script := scriptArg(t, cap)
	if !strings.Contains(script, "example.com") {
		t.Errorf("generated script missing target host: %s", script)
	}
}

// TestBrowserNavigateEscapesInjectionAttempt is the core regression test: a URL
// engineered to break out of the single-quoted Python literal must not be able
// to append arbitrary statements to the generated script.
func TestBrowserNavigateEscapesInjectionAttempt(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	// A real navigate would reject this (only http/https survive ValidateURL),
	// but the escaping must hold even for a value that got this far — defence
	// in depth against a future policy bug.
	malicious := "https://example.com/'); import os; os.system('id'); print('"

	_, _ = srv.toolBrowserNavigate(context.Background(), map[string]string{
		"session_id": "s1",
		"url":        malicious,
	})

	if cap.lastRun.Command == "" {
		// ValidateURL rejected it outright — the safest possible outcome, and
		// the escaping test below is then moot for this exact input.
		return
	}
	script := scriptArg(t, cap)
	assertFieldEscaped(t, "navigate", script, malicious)
}

func TestBrowserNavigateRejectsBadWaitUntil(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	_, err := srv.toolBrowserNavigate(context.Background(), map[string]string{
		"session_id": "s1",
		"url":        "https://example.com",
		"wait_until": "'; import os; os.system('id')",
	})
	if err == nil {
		t.Error("invalid wait_until: want error, got nil")
	}
}

func TestBrowserNavigateRejectsNonNumericTimeout(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	_, err := srv.toolBrowserNavigate(context.Background(), map[string]string{
		"session_id": "s1",
		"url":        "https://example.com",
		"timeout":    "1); import os; os.system('id'); (",
	})
	if err == nil {
		t.Error("non-numeric timeout: want error, got nil")
	}
}

func TestBrowserNavigateClampsExcessiveTimeout(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	if _, err := srv.toolBrowserNavigate(context.Background(), map[string]string{
		"session_id": "s1",
		"url":        "https://example.com",
		"timeout":    "99999999",
	}); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	script := scriptArg(t, cap)
	if !strings.Contains(script, "set_default_timeout(120000)") {
		t.Errorf("timeout not clamped to max, script: %s", script)
	}
}

// ---------------------------------------------------------------------------
// browser_click / browser_fill: selector and value injection
// ---------------------------------------------------------------------------

func TestBrowserClickEscapesSelector(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	malicious := `'); import os; os.system('id'); page.locator('`
	if _, err := srv.toolBrowserClick(context.Background(), map[string]string{
		"session_id": "s1",
		"selector":   malicious,
	}); err != nil {
		t.Fatalf("click: %v", err)
	}

	script := scriptArg(t, cap)
	assertFieldEscaped(t, "click", script, malicious)
}

func TestBrowserClickRequiresArgs(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	if _, err := srv.toolBrowserClick(context.Background(), map[string]string{"session_id": "s1"}); err == nil {
		t.Error("missing selector: want error, got nil")
	}
}

func TestBrowserFillEscapesSelectorAndValue(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	maliciousSelector := `input[name='x']`
	maliciousValue := `'); import subprocess; subprocess.run(['rm','-rf','/']); x=('`
	if _, err := srv.toolBrowserFill(context.Background(), map[string]string{
		"session_id": "s1",
		"selector":   maliciousSelector,
		"value":      maliciousValue,
	}); err != nil {
		t.Fatalf("fill: %v", err)
	}

	script := scriptArg(t, cap)
	assertFieldEscaped(t, "fill selector", script, maliciousSelector)
	assertFieldEscaped(t, "fill value", script, maliciousValue)
}

func TestBrowserFillRequiresAllArgs(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	if _, err := srv.toolBrowserFill(context.Background(), map[string]string{
		"session_id": "s1", "selector": "#x",
	}); err == nil {
		t.Error("missing value: want error, got nil")
	}
}

// ---------------------------------------------------------------------------
// browser_extract
// ---------------------------------------------------------------------------

func TestBrowserExtractDefaultsToFullPageText(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	if _, err := srv.toolBrowserExtract(context.Background(), map[string]string{"session_id": "s1"}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	script := scriptArg(t, cap)
	if !strings.Contains(script, "content = page.content()") {
		t.Errorf("expected full-page extraction, got: %s", script)
	}
}

func TestBrowserExtractRejectsUnknownType(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	_, err := srv.toolBrowserExtract(context.Background(), map[string]string{
		"session_id": "s1",
		"extract":    "attributes",
	})
	if err == nil {
		t.Error("invalid extract type: want error, got nil")
	}
}

func TestBrowserExtractEscapesSelector(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	malicious := `'); import os; os.system('id'); x=('`
	if _, err := srv.toolBrowserExtract(context.Background(), map[string]string{
		"session_id": "s1",
		"selector":   malicious,
		"extract":    "html",
	}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	script := scriptArg(t, cap)
	assertFieldEscaped(t, "extract", script, malicious)
}

// ---------------------------------------------------------------------------
// browser_evaluate: JS is base64-encoded, so no escaping bug is even possible
// ---------------------------------------------------------------------------

func TestBrowserEvaluateBase64EncodesScript(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	jsPayload := `'; }); __import__('os').system('id'); ({`
	if _, err := srv.toolBrowserEvaluate(context.Background(), map[string]string{
		"session_id": "s1",
		"script":     jsPayload,
	}); err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	script := scriptArg(t, cap)
	if strings.Contains(script, "__import__") {
		t.Errorf("raw JS leaked into generated Python unescaped: %s", script)
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte(jsPayload))
	if !strings.Contains(script, wantB64) {
		t.Errorf("expected base64 payload %q in script: %s", wantB64, script)
	}
}

func TestBrowserEvaluateRequiresScript(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	if _, err := srv.toolBrowserEvaluate(context.Background(), map[string]string{"session_id": "s1"}); err == nil {
		t.Error("missing script: want error, got nil")
	}
}

// ---------------------------------------------------------------------------
// browser_wait
// ---------------------------------------------------------------------------

func TestBrowserWaitRejectsNonNumericTimeout(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	_, err := srv.toolBrowserWait(context.Background(), map[string]string{
		"session_id": "s1",
		"timeout":    "'); os.system('id'); (",
	})
	if err == nil {
		t.Error("non-numeric timeout: want error, got nil")
	}
}

func TestBrowserWaitEscapesSelector(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	malicious := `'); import os; os.system('id'); x=('`
	if _, err := srv.toolBrowserWait(context.Background(), map[string]string{
		"session_id": "s1",
		"selector":   malicious,
	}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	script := scriptArg(t, cap)
	assertFieldEscaped(t, "wait", script, malicious)
}

func TestBrowserWaitWithoutSelectorUsesTimeout(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	if _, err := srv.toolBrowserWait(context.Background(), map[string]string{"session_id": "s1"}); err != nil {
		t.Fatalf("wait: %v", err)
	}
	script := scriptArg(t, cap)
	if !strings.Contains(script, "wait_for_timeout(30000)") {
		t.Errorf("expected default timeout wait, got: %s", script)
	}
}

// ---------------------------------------------------------------------------
// browser_screenshot / browser_pdf: path confinement to /workspace
// ---------------------------------------------------------------------------

func TestBrowserScreenshotConfinesPathToWorkspace(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"default", "", false},
		{"explicit workspace path", "/workspace/shot.png", false},
		{"relative path", "shot.png", false},
		{"traversal to etc", "/workspace/../../etc/cron.d/evil", true},
		{"absolute outside workspace", "/etc/passwd", true},
		// path.Join cleans ".." against the workspace prefix before the
		// confinement check runs, so this also resolves outside /workspace
		// and must be refused rather than silently accepted.
		{"traversal within relative resolves outside workspace", "../../etc/passwd", true},
		{"NUL byte", "/workspace/shot.png\x00.txt", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &browserCapture{}
			srv, cleanup := newBrowserTestServer(t, cap)
			defer cleanup()

			args := map[string]string{"session_id": "s1"}
			if tc.path != "" {
				args["path"] = tc.path
			}
			_, err := srv.toolBrowserScreenshot(context.Background(), args)
			if tc.wantErr && err == nil {
				t.Fatalf("path %q: want error, got nil (script: %s)", tc.path, cap.lastRun.Args)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("path %q: unexpected error: %v", tc.path, err)
			}
			if !tc.wantErr {
				// The generated script must still confine writes under /workspace.
				script := scriptArg(t, cap)
				if !strings.Contains(script, "/workspace") {
					t.Errorf("path %q: expected script to write under /workspace: %s", tc.path, script)
				}
			}
		})
	}
}

func TestBrowserScreenshotRejectsBadFullPageIsIgnoredNotInjected(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	// full_page is not an allowlisted enum in the handler; anything other than
	// the literal "true" must fall back to False rather than being interpolated.
	if _, err := srv.toolBrowserScreenshot(context.Background(), map[string]string{
		"session_id": "s1",
		"full_page":  "True); import os; os.system('id'); (False",
	}); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	script := scriptArg(t, cap)
	if !strings.Contains(script, "full_page=False") {
		t.Errorf("full_page value was interpolated instead of mapped to a boolean: %s", script)
	}
}

func TestBrowserPDFConfinesPathAndValidatesFormat(t *testing.T) {
	cap := &browserCapture{}
	srv, cleanup := newBrowserTestServer(t, cap)
	defer cleanup()

	if _, err := srv.toolBrowserPDF(context.Background(), map[string]string{
		"session_id": "s1",
		"path":       "/etc/cron.d/evil",
	}); err == nil {
		t.Error("path escaping /workspace: want error, got nil")
	}

	if _, err := srv.toolBrowserPDF(context.Background(), map[string]string{
		"session_id": "s1",
		"format":     "'); import os; os.system('id'); (",
	}); err == nil {
		t.Error("invalid format: want error, got nil")
	}

	if _, err := srv.toolBrowserPDF(context.Background(), map[string]string{
		"session_id": "s1",
	}); err != nil {
		t.Fatalf("pdf with defaults: %v", err)
	}
	script := scriptArg(t, cap)
	if !strings.Contains(script, "format='A4'") {
		t.Errorf("expected default A4 format, got: %s", script)
	}
}

func TestBrowserRequireSessionID(t *testing.T) {
	srv, cleanup := newBrowserTestServer(t, &browserCapture{})
	defer cleanup()

	tools := map[string]func(context.Context, map[string]string) (mcpToolResult, error){
		"navigate":   srv.toolBrowserNavigate,
		"screenshot": srv.toolBrowserScreenshot,
		"click":      srv.toolBrowserClick,
		"fill":       srv.toolBrowserFill,
		"extract":    srv.toolBrowserExtract,
		"evaluate":   srv.toolBrowserEvaluate,
		"wait":       srv.toolBrowserWait,
		"pdf":        srv.toolBrowserPDF,
	}

	for name, fn := range tools {
		t.Run(name, func(t *testing.T) {
			args := map[string]string{"url": "https://example.com", "selector": "#x", "value": "y", "script": "1"}
			if _, err := fn(context.Background(), args); err == nil {
				t.Errorf("%s without session_id: want error, got nil", name)
			}
		})
	}
}
