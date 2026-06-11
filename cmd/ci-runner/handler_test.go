package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidateSignature(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"action":"opened"}`)
	good := sign(secret, body)

	if !validateSignature(secret, good, body) {
		t.Error("valid signature rejected")
	}
	if validateSignature(secret, "sha256=deadbeef", body) {
		t.Error("bad hex signature accepted")
	}
	if validateSignature(secret, "md5="+good[7:], body) {
		t.Error("wrong algorithm accepted")
	}
	if validateSignature(secret, good, []byte("tampered")) {
		t.Error("tampered body accepted")
	}
}

func TestParsePREvent(t *testing.T) {
	cases := []struct {
		name      string
		action    string
		wantParse bool
	}{
		{"opened", "opened", true},
		{"synchronize", "synchronize", true},
		{"reopened", "reopened", true},
		{"closed", "closed", false},
		{"labeled", "labeled", false},
		{"review_requested", "review_requested", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"action": tc.action,
				"number": 42,
				"pull_request": map[string]any{
					"head": map[string]string{
						"sha": "abc1234567890",
						"ref": "feature-branch",
					},
				},
				"repository": map[string]string{"full_name": "owner/repo"},
				"sender":     map[string]string{"login": "alice"},
			}
			body, _ := json.Marshal(payload)
			pr, ok := parsePREvent(body)
			if ok != tc.wantParse {
				t.Fatalf("action %q: wantParse=%v got=%v", tc.action, tc.wantParse, ok)
			}
			if !ok {
				return
			}
			if pr.owner != "owner" || pr.repo != "repo" {
				t.Errorf("owner/repo: got %s/%s", pr.owner, pr.repo)
			}
			if pr.number != 42 {
				t.Errorf("number: got %d", pr.number)
			}
			if pr.sha != "abc1234567890" {
				t.Errorf("sha: got %q", pr.sha)
			}
			if pr.branch != "feature-branch" {
				t.Errorf("branch: got %q", pr.branch)
			}
			if pr.sender != "alice" {
				t.Errorf("sender: got %q", pr.sender)
			}
		})
	}
}

func TestParsePREventMalformed(t *testing.T) {
	cases := [][]byte{
		[]byte(`{}`),
		[]byte(`{"action":"opened","number":1,"pull_request":{"head":{"sha":"","ref":""}},"repository":{"full_name":"owner/repo"}}`),
		[]byte(`{"action":"opened","number":1,"pull_request":{"head":{"sha":"abc","ref":"main"}},"repository":{"full_name":"noslash"}}`),
		[]byte(`not json`),
	}
	for _, body := range cases {
		if _, ok := parsePREvent(body); ok {
			t.Errorf("malformed payload accepted: %s", body)
		}
	}
}

func TestBuildComment(t *testing.T) {
	pr := prRun{owner: "acme", repo: "app", number: 7, sha: "deadbeef1234", branch: "fix/bug"}
	steps := []stepResult{
		{name: "Clone", passed: true, output: "Cloning..."},
		{name: "`make test`", passed: false, output: "FAIL: 2 tests failed"},
	}
	comment := buildComment(pr, steps, false)

	for _, want := range []string{"❌", "acme/app", "deadbeef", "fix/bug", "make test", "FAIL: 2 tests"} {
		if !contains(comment, want) {
			t.Errorf("comment missing %q", want)
		}
	}
}

func TestBuildCommentAllPass(t *testing.T) {
	pr := prRun{owner: "o", repo: "r", number: 1, sha: "aabbccdd1234", branch: "main"}
	steps := []stepResult{
		{name: "`go test ./...`", passed: true, output: "ok"},
	}
	comment := buildComment(pr, steps, true)
	if !contains(comment, "✅") {
		t.Error("passing comment missing ✅")
	}
}

func TestScrubToken(t *testing.T) {
	s := scrubToken("clone https://x-access-token:mysecret@github.com", "mysecret")
	if contains(s, "mysecret") {
		t.Error("token not scrubbed")
	}
	if !contains(s, "[REDACTED]") {
		t.Error("REDACTED not present")
	}
	// Empty token: no-op
	if scrubToken("hello", "") != "hello" {
		t.Error("empty token should be no-op")
	}
}

func TestTruncate(t *testing.T) {
	s := truncate("hello", 3)
	if !contains(s, "hel") {
		t.Error("truncated prefix missing")
	}
	if !contains(s, "truncated") {
		t.Error("truncated notice missing")
	}
	if truncate("short", 100) != "short" {
		t.Error("short string should be unchanged")
	}
}

func TestLoadConfigMissingRequired(t *testing.T) {
	t.Setenv("VAULTRUN_BASE_URL", "")
	t.Setenv("VAULTRUN_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for missing config, got nil")
	}
}

func TestLoadConfigBadCommands(t *testing.T) {
	t.Setenv("VAULTRUN_BASE_URL", "http://localhost")
	t.Setenv("VAULTRUN_API_KEY", "key")
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "sec")
	t.Setenv("CI_TEST_COMMANDS", "not-json")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for invalid CI_TEST_COMMANDS")
	}
	t.Setenv("CI_TEST_COMMANDS", "[]")
	if _, err := loadConfig(); err == nil {
		t.Error("expected error for empty CI_TEST_COMMANDS")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
