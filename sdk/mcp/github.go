// GitHub API client for the MCP server.
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
	return &githubClient{token: token, httpClient: &http.Client{}}
}

func (g *githubClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, githubAPIBase+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (g *githubClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := g.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Try to extract "message" field from GitHub error JSON.
		var errBody struct {
			Message string `json:"message"`
		}
		if e := json.Unmarshal(raw, &errBody); e == nil && errBody.Message != "" {
			return fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, errBody.Message)
		}
		return fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// reValidGitHubName validates owner and repo name components.
var reValidGitHubName = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,100}$`)

// reValidGitRef validates a git ref (branch, tag, etc.).
var reValidGitRef = regexp.MustCompile(`^[A-Za-z0-9_./\-]{1,255}$`)

// validateGitRef validates a git ref against the whitelist regex and rejects "..".
func validateGitRef(s string) error {
	if s == "" {
		return fmt.Errorf("git ref must not be empty")
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("git ref %q: contains '..' which is not allowed", s)
	}
	if !reValidGitRef.MatchString(s) {
		return fmt.Errorf("git ref %q: contains invalid characters (allowed: A-Za-z0-9_./- up to 255 chars)", s)
	}
	return nil
}

// parseOwnerRepo splits "owner/repo" and validates both parts against reValidGitHubName.
func parseOwnerRepo(s string) (owner, repo string, err error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("repo %q: must be in owner/repo format (exactly one slash)", s)
	}
	owner, repo = parts[0], parts[1]
	if !reValidGitHubName.MatchString(owner) {
		return "", "", fmt.Errorf("repo %q: invalid owner name (allowed: A-Za-z0-9_.- up to 100 chars)", s)
	}
	if !reValidGitHubName.MatchString(repo) {
		return "", "", fmt.Errorf("repo %q: invalid repo name (allowed: A-Za-z0-9_.- up to 100 chars)", s)
	}
	return owner, repo, nil
}

// defaultBranch fetches the default branch for a GitHub repository.
func (g *githubClient) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	var result struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return "", err
	}
	if result.DefaultBranch == "" {
		return "", fmt.Errorf("no default_branch returned for %s/%s", owner, repo)
	}
	return result.DefaultBranch, nil
}

// postComment posts a comment on a GitHub issue or pull request.
// Returns the URL of the created comment.
func (g *githubClient) postComment(ctx context.Context, owner, repo string, number int, body string) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), number)

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := g.doJSON(ctx, http.MethodPost, path, map[string]string{"body": body}, &result); err != nil {
		return "", err
	}
	return result.HTMLURL, nil
}

// scrubToken replaces all occurrences of token in s with "[REDACTED]".
// If token is empty, s is returned unchanged.
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}
