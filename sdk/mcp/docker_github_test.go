package main

// Security and edge-case tests for Docker tools and GitHub integration.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// validateGitRef
// ---------------------------------------------------------------------------

func TestValidateGitRef(t *testing.T) {
	good := []string{
		"main", "develop", "feature/my-thing", "v1.2.3", "release-candidate",
		"refs/heads/main", "sha256-abc123", "tag_v1", "1.0.0",
	}
	for _, ref := range good {
		if err := validateGitRef(ref); err != nil {
			t.Errorf("validateGitRef(%q): unexpected error: %v", ref, err)
		}
	}

	bad := []struct {
		ref     string
		wantErr string
	}{
		{"main; rm -rf /", "invalid characters"},
		{"main$(whoami)", "invalid characters"},
		{"main`id`", "invalid characters"},
		{"main && cat /etc/passwd", "invalid characters"},
		{"main | tee /tmp/x", "invalid characters"},
		{"../../../etc/passwd", "'..' not allowed"},
		{"feature..evil", "'..' not allowed"},
		{"", "invalid characters"},
		{strings.Repeat("a", 256), "invalid characters"},
		{"main\x00injected", "invalid characters"},
		{"main\ninjected", "invalid characters"},
		{"main space", "invalid characters"},
		{"ref with\ttab", "invalid characters"},
	}
	for _, tc := range bad {
		err := validateGitRef(tc.ref)
		if err == nil {
			t.Errorf("validateGitRef(%q): expected error containing %q, got nil", tc.ref, tc.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("validateGitRef(%q): expected %q in error, got: %v", tc.ref, tc.wantErr, err)
		}
	}
}

// ---------------------------------------------------------------------------
// parseOwnerRepo
// ---------------------------------------------------------------------------

func TestParseOwnerRepo(t *testing.T) {
	good := []struct{ input, owner, repo string }{
		{"nickvd7/vaultrun", "nickvd7", "vaultrun"},
		{"org-name/repo_name", "org-name", "repo_name"},
		{"A/B.C", "A", "B.C"},
	}
	for _, tc := range good {
		o, r, err := parseOwnerRepo(tc.input)
		if err != nil || o != tc.owner || r != tc.repo {
			t.Errorf("parseOwnerRepo(%q) = (%q,%q,%v), want (%q,%q,nil)", tc.input, o, r, err, tc.owner, tc.repo)
		}
	}

	bad := []struct {
		input   string
		wantErr string
	}{
		{"notvalid", "owner/repo format"},
		{"", "owner/repo format"},
		{"/repo", "owner/repo format"},
		{"owner/", "owner/repo format"},
		{"../etc/passwd", "invalid characters"},   // owner=".." repo="etc/passwd" → repo has slash
		{"../../etc/passwd", "invalid characters"}, // owner=".." repo="../passwd" → both invalid
		{"owner with space/repo", "invalid characters"},
		{"owner;rm/repo", "invalid characters"},
		{"owner$(id)/repo", "invalid characters"},
		{strings.Repeat("a", 101) + "/repo", "invalid characters"},
	}
	for _, tc := range bad {
		_, _, err := parseOwnerRepo(tc.input)
		if err == nil {
			t.Errorf("parseOwnerRepo(%q): expected error containing %q, got nil", tc.input, tc.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("parseOwnerRepo(%q): expected %q in error, got: %v", tc.input, tc.wantErr, err)
		}
	}
}

// ---------------------------------------------------------------------------
// scrubToken
// ---------------------------------------------------------------------------

func TestScrubToken(t *testing.T) {
	token := "ghp_super_secret_12345"
	cases := []struct {
		input string
		want  string
	}{
		{"no token here", "no token here"},
		{"fatal: clone failed: https://x-access-token:" + token + "@github.com/", "fatal: clone failed: https://x-access-token:[REDACTED]@github.com/"},
		{token + " appears twice " + token, "[REDACTED] appears twice [REDACTED]"},
		{"", ""},
	}
	for _, tc := range cases {
		got := scrubToken(tc.input, token)
		if got != tc.want {
			t.Errorf("scrubToken(%q, ...) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestScrubTokenEmptyToken(t *testing.T) {
	s := "some output with no token"
	if got := scrubToken(s, ""); got != s {
		t.Errorf("scrubToken with empty token modified string: got %q", got)
	}
}

// ---------------------------------------------------------------------------
// toolGithubPostComment validation
// ---------------------------------------------------------------------------

func TestGithubPostCommentValidation(t *testing.T) {
	srv := newTestServer()

	cases := []struct {
		name    string
		args    map[string]string
		wantErr string
	}{
		{"missing repo", map[string]string{"number": "1", "body": "hi"}, "repo is required"},
		{"missing body", map[string]string{"repo": "o/r", "number": "1"}, "body is required"},
		{"missing number", map[string]string{"repo": "o/r", "body": "hi"}, "number is required"},
		{"number zero", map[string]string{"repo": "o/r", "number": "0", "body": "hi"}, "positive integer"},
		{"number negative", map[string]string{"repo": "o/r", "number": "-5", "body": "hi"}, "positive integer"},
		{"number too large", map[string]string{"repo": "o/r", "number": "1000001", "body": "hi"}, "positive integer"},
		{"number NaN", map[string]string{"repo": "o/r", "number": "abc", "body": "hi"}, "positive integer"},
		{"invalid repo", map[string]string{"repo": "notvalid", "number": "1", "body": "hi"}, "owner/repo format"},
		{"injection in owner", map[string]string{"repo": "evil;cmd/repo", "number": "1", "body": "hi"}, "invalid characters"},
		{"body too long", map[string]string{"repo": "o/r", "number": "1", "body": strings.Repeat("x", 65537)}, "too long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.callTool(context.Background(), "github_post_comment",
				func() json.RawMessage { b, _ := json.Marshal(tc.args); return b }())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toolRunGithubRepo validation (no network needed — fails before API calls)
// ---------------------------------------------------------------------------

func TestRunGithubRepoValidation(t *testing.T) {
	srv := newTestServer()

	cases := []struct {
		name    string
		args    map[string]string
		wantErr string
	}{
		{"missing repo", nil, "repo is required"},
		{"invalid owner injection", map[string]string{"repo": "evil;cmd/repo"}, "invalid characters"},
		{"path traversal owner", map[string]string{"repo": "../../etc/passwd"}, "invalid characters"},
		{"invalid repo name", map[string]string{"repo": "owner/repo with space"}, "invalid characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			if tc.args != nil {
				raw, _ = json.Marshal(tc.args)
			} else {
				raw = json.RawMessage(`{}`)
			}
			_, err := srv.callTool(context.Background(), "run_github_repo", raw)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestRunGithubRepoBranchInjectionBlocked(t *testing.T) {
	// A mock VaultRun API that captures what commands are actually sent.
	var capturedArgs [][]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "sess-test", "image": "python:3.12-slim", "status": "created",
				"created_at": time.Now(),
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/run"):
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if args, ok := req["args"].([]any); ok {
				strs := make([]string, len(args))
				for i, a := range args {
					strs[i], _ = a.(string)
				}
				capturedArgs = append(capturedArgs, strs)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-1", "session_id": "sess-test", "command": "git",
				"status": "completed", "exit_code": 0,
				"stdout": "Cloning into '/workspace/repo'...",
			})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	srv := newServer(newVaultRunClient(ts.URL, "k"), "python:3.12-slim", "")

	// Attempt branch injection — the payload tries to escape and run a second command.
	injectedBranch := "main; cat /etc/passwd > /tmp/pwned"
	args, _ := json.Marshal(map[string]string{
		"repo":   "nickvd7/vaultrun",
		"branch": injectedBranch,
	})
	_, err := srv.callTool(context.Background(), "run_github_repo", args)
	// validateGitRef should reject this before any API call is made.
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("expected branch injection to be rejected, got: %v", err)
	}
	// Ensure no git clone was actually called (no capturedArgs set from /run endpoint).
	if len(capturedArgs) > 0 {
		t.Errorf("git clone was called despite invalid branch — injection not blocked: %v", capturedArgs)
	}
}

func TestRunGithubRepoCommandsLimit(t *testing.T) {
	// Build a mock that accepts session creation and run calls.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "s1", "image": "python:3.12-slim", "status": "created",
				"created_at": time.Now(),
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/run"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "r1", "session_id": "s1", "command": "git",
				"status": "completed", "exit_code": 0, "stdout": "ok",
			})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	srv := newServer(newVaultRunClient(ts.URL, "k"), "python:3.12-slim", "")

	// 51 commands should be rejected.
	cmds := make([]string, 51)
	for i := range cmds {
		cmds[i] = "echo hello"
	}
	cmdsJSON, _ := json.Marshal(cmds)
	args, _ := json.Marshal(map[string]string{
		"repo":     "nickvd7/vaultrun",
		"branch":   "main",
		"commands": string(cmdsJSON),
	})
	_, err := srv.callTool(context.Background(), "run_github_repo", args)
	if err == nil || !strings.Contains(err.Error(), "at most 50 commands") {
		t.Errorf("expected commands limit error, got: %v", err)
	}
}

func TestRunGithubRepoCommandTooLong(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "s1", "image": "python:3.12-slim", "status": "created",
				"created_at": time.Now(),
			})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/run"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "r1", "session_id": "s1", "command": "git",
				"status": "completed", "exit_code": 0, "stdout": "ok",
			})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	srv := newServer(newVaultRunClient(ts.URL, "k"), "python:3.12-slim", "")

	longCmd := strings.Repeat("x", 4097)
	cmdsJSON, _ := json.Marshal([]string{longCmd})
	args, _ := json.Marshal(map[string]string{
		"repo":     "nickvd7/vaultrun",
		"branch":   "main",
		"commands": string(cmdsJSON),
	})
	_, err := srv.callTool(context.Background(), "run_github_repo", args)
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("expected command-too-long error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// get_session_logs tail validation
// ---------------------------------------------------------------------------

func TestGetSessionLogsTailValidation(t *testing.T) {
	srv := newTestServer()

	bad := []string{"0", "-1", "10001", "abc", "9999999999"}
	for _, v := range bad {
		args, _ := json.Marshal(map[string]string{"session_id": "s1", "tail": v})
		_, err := srv.callTool(context.Background(), "get_session_logs", args)
		if err == nil || !strings.Contains(err.Error(), "tail must be") {
			t.Errorf("tail=%q: expected validation error, got: %v", v, err)
		}
	}
}

// ---------------------------------------------------------------------------
// pull_image validation
// ---------------------------------------------------------------------------

func TestPullImageMissingParam(t *testing.T) {
	srv := newTestServer()
	_, err := srv.callTool(context.Background(), "pull_image", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Errorf("expected 'image is required', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// list_images — empty list
// ---------------------------------------------------------------------------

func TestListImagesEmpty(t *testing.T) {
	srv, ts := newMockServer(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"images": []any{}, "total": 0})
	})
	defer ts.Close()

	res, err := srv.callTool(context.Background(), "list_images", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "No local") {
		t.Errorf("expected empty-list message, got: %s", res.Content[0].Text)
	}
}

// ---------------------------------------------------------------------------
// get_session_stats — session_id required
// ---------------------------------------------------------------------------

func TestGetSessionStatsMissingSessionID(t *testing.T) {
	srv := newTestServer()
	_, err := srv.callTool(context.Background(), "get_session_stats", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "session_id is required") {
		t.Errorf("expected session_id required error, got: %v", err)
	}
}
