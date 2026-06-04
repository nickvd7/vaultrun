// GitHub API helper — used by the run_github_repo and github_post_comment tools.
// Only the subset of the GitHub REST API needed for these tools is implemented.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const githubAPIBase = "https://api.github.com"

type githubClient struct {
	token      string
	httpClient *http.Client
}

func newGithubClient(token string) *githubClient {
	return &githubClient{
		token:      token,
		httpClient: &http.Client{},
	}
}

func (g *githubClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal github request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	url := githubAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	return g.httpClient.Do(req)
}

func (g *githubClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := g.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errBody struct {
			Message string `json:"message"`
		}
		if e := json.Unmarshal(raw, &errBody); e == nil && errBody.Message != "" {
			return fmt.Errorf("GitHub API %d: %s", resp.StatusCode, errBody.Message)
		}
		return fmt.Errorf("GitHub API %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// reValidGitHubName allows only the characters GitHub itself permits in owner
// and repository names: alphanumeric, hyphens, underscores, and dots.
var reValidGitHubName = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,100}$`)

// reValidGitRef matches legal git ref components: alphanumeric, hyphens,
// underscores, dots, and slashes (for branch namespaces like refs/heads/…).
// Rejects anything that could be interpreted by a shell.
var reValidGitRef = regexp.MustCompile(`^[A-Za-z0-9_.\/\-]{1,255}$`)

// validateGitRef returns an error if s is not a safe git ref name.
func validateGitRef(s string) error {
	if !reValidGitRef.MatchString(s) {
		return fmt.Errorf("branch/tag %q contains invalid characters (allowed: A-Z a-z 0-9 _ . / -)", s)
	}
	// Reject git's special ".." notation which traverses ref history.
	if strings.Contains(s, "..") {
		return fmt.Errorf("branch/tag %q: '..' not allowed", s)
	}
	return nil
}

// parseOwnerRepo splits "owner/repo" into owner and repo and validates both
// components against GitHub's naming rules.
func parseOwnerRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be in owner/repo format, got %q", s)
	}
	if !reValidGitHubName.MatchString(parts[0]) {
		return "", "", fmt.Errorf("owner %q contains invalid characters", parts[0])
	}
	if !reValidGitHubName.MatchString(parts[1]) {
		return "", "", fmt.Errorf("repo name %q contains invalid characters", parts[1])
	}
	return parts[0], parts[1], nil
}

// defaultBranch returns the default branch for owner/repo.
func (g *githubClient) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var result struct {
		DefaultBranch string `json:"default_branch"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	if err := g.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", err
	}
	if result.DefaultBranch == "" {
		return "main", nil
	}
	return result.DefaultBranch, nil
}

// postComment posts a comment on an issue or PR (they share the same API endpoint).
func (g *githubClient) postComment(ctx context.Context, owner, repo string, number int, body string) (string, error) {
	var result struct {
		HTMLURL string `json:"html_url"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), number)
	if err := g.doJSON(ctx, http.MethodPost, path, map[string]string{"body": body}, &result); err != nil {
		return "", err
	}
	return result.HTMLURL, nil
}

// scrubToken replaces all occurrences of token in s with [REDACTED].
// A no-op when token is empty.
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}
