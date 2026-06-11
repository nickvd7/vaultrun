// Tests for Docker/GitHub validation logic.
package main

import (
	"context"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// validateGitRef
// ---------------------------------------------------------------------------

func TestValidateGitRef(t *testing.T) {
	valid := []string{
		"main",
		"master",
		"v1.0.0",
		"feat/branch",
		"release/1.2.3",
		"abc123",
		"A-Za-z0-9",
	}
	for _, ref := range valid {
		if err := validateGitRef(ref); err != nil {
			t.Errorf("validateGitRef(%q) should be valid: %v", ref, err)
		}
	}

	invalid := []struct {
		ref  string
		desc string
	}{
		{"", "empty string"},
		{"; rm -rf /", "shell injection"},
		{"../../../etc", "path traversal with dots"},
		{"a b c", "spaces"},
		{strings.Repeat("a", 256), "too long"},
		{"ref..other", "contains '..'"},
		{"HEAD..main", "contains '..'"},
	}
	for _, tc := range invalid {
		if err := validateGitRef(tc.ref); err == nil {
			t.Errorf("validateGitRef(%q) should be invalid (%s)", tc.ref, tc.desc)
		}
	}
}

// ---------------------------------------------------------------------------
// parseOwnerRepo
// ---------------------------------------------------------------------------

func TestParseOwnerRepo(t *testing.T) {
	valid := []struct {
		input         string
		wantOwner     string
		wantRepo      string
	}{
		{"owner/repo", "owner", "repo"},
		{"my-org/my-repo", "my-org", "my-repo"},
		{"User123/Project.name", "User123", "Project.name"},
	}
	for _, tc := range valid {
		owner, repo, err := parseOwnerRepo(tc.input)
		if err != nil {
			t.Errorf("parseOwnerRepo(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if owner != tc.wantOwner || repo != tc.wantRepo {
			t.Errorf("parseOwnerRepo(%q) = (%q, %q), want (%q, %q)",
				tc.input, owner, repo, tc.wantOwner, tc.wantRepo)
		}
	}

	invalid := []struct {
		input string
		desc  string
	}{
		{"no-slash", "no slash"},
		{"/repo", "empty owner"},
		{"owner/", "empty repo"},
		{"owner/repo/extra", "too many slashes"},
		{"../owner/repo", "traversal in owner"},
		{"owner with spaces/repo", "spaces in owner"},
		{"owner/repo with spaces", "spaces in repo"},
	}
	for _, tc := range invalid {
		if _, _, err := parseOwnerRepo(tc.input); err == nil {
			t.Errorf("parseOwnerRepo(%q) should fail (%s)", tc.input, tc.desc)
		}
	}
}

// ---------------------------------------------------------------------------
// scrubToken
// ---------------------------------------------------------------------------

func TestScrubToken(t *testing.T) {
	result := scrubToken("hello secret world", "secret")
	if result != "hello [REDACTED] world" {
		t.Errorf("scrubToken: expected 'hello [REDACTED] world', got %q", result)
	}
}

func TestScrubTokenEmpty(t *testing.T) {
	input := "hello world"
	result := scrubToken(input, "")
	if result != input {
		t.Errorf("scrubToken with empty token: expected input unchanged, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// toolGithubPostComment validation
// ---------------------------------------------------------------------------

func TestGithubPostCommentValidation(t *testing.T) {
	srv := newTestServer() // nil client; validation runs before any API call
	ctx := context.Background()

	cases := []struct {
		name    string
		args    map[string]string
		wantErr string
	}{
		{
			"body too long",
			map[string]string{
				"repo":   "owner/repo",
				"number": "1",
				"body":   strings.Repeat("x", 65537),
			},
			"too long",
		},
		{
			"number zero",
			map[string]string{"repo": "owner/repo", "number": "0", "body": "hi"},
			"positive integer",
		},
		{
			"number negative",
			map[string]string{"repo": "owner/repo", "number": "-1", "body": "hi"},
			"positive integer",
		},
		{
			"number too large",
			map[string]string{"repo": "owner/repo", "number": "100000001", "body": "hi"},
			"positive integer",
		},
		{
			"number not a number",
			map[string]string{"repo": "owner/repo", "number": "abc", "body": "hi"},
			"positive integer",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.toolGithubPostComment(ctx, tc.args)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toolRunGithubRepo validation
// ---------------------------------------------------------------------------

func TestRunGithubRepoValidation(t *testing.T) {
	srv := newTestServer() // nil client
	ctx := context.Background()

	cases := []struct {
		name    string
		args    map[string]string
		wantErr string
	}{
		{
			"no repo",
			map[string]string{"commands": `[["python","main.py"]]`},
			"repo is required",
		},
		{
			"invalid repo format",
			map[string]string{"repo": "not-valid-format", "commands": `[["python","main.py"]]`},
			"owner/repo format",
		},
		{
			"too many commands",
			map[string]string{
				"repo":     "owner/repo",
				"commands": buildCommandsJSON(51),
			},
			"max 50",
		},
		{
			"command too long",
			map[string]string{
				"repo":     "owner/repo",
				"commands": `[["` + strings.Repeat("a", 4097) + `"]]`,
			},
			"too long",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.toolRunGithubRepo(ctx, tc.args)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// buildCommandsJSON creates a JSON array of n simple commands for testing.
func buildCommandsJSON(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `["echo","x"]`
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TestRunGithubRepoBranchInjection: an invalid branch is rejected BEFORE
// any API call is made (nil client means any network call would panic).
func TestRunGithubRepoBranchInjection(t *testing.T) {
	srv := newTestServer() // nil client — any API call would panic
	ctx := context.Background()

	_, err := srv.toolRunGithubRepo(ctx, map[string]string{
		"repo":     "owner/repo",
		"commands": `[["echo","x"]]`,
		"branch":   "; rm -rf /",
	})
	if err == nil {
		t.Fatal("expected error for branch injection, got nil")
	}
	// Verify the error mentions the invalid characters, not a nil pointer panic.
	if strings.Contains(err.Error(), "nil pointer") || strings.Contains(err.Error(), "panic") {
		t.Errorf("unexpected panic instead of validation error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// toolGetSessionLogs tail validation
// ---------------------------------------------------------------------------

func TestGetSessionLogsTailValidation(t *testing.T) {
	srv := newTestServer() // nil client
	ctx := context.Background()

	invalidTails := []struct {
		tail    string
		wantErr string
	}{
		{"0", "between 1 and 10000"},
		{"-1", "between 1 and 10000"},
		{"10001", "between 1 and 10000"},
		{"abc", "between 1 and 10000"},
	}

	for _, tc := range invalidTails {
		t.Run("tail="+tc.tail, func(t *testing.T) {
			_, err := srv.toolGetSessionLogs(ctx, map[string]string{
				"session_id": "sess-1",
				"tail":       tc.tail,
			})
			if err == nil {
				t.Fatalf("expected error for tail=%q, got nil", tc.tail)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}
