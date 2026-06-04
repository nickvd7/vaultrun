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

// defaultBranch returns the default branch for owner/repo.
func (g *githubClient) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var result struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.doJSON(ctx, http.MethodGet, "/repos/"+owner+"/"+repo, nil, &result); err != nil {
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
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := g.doJSON(ctx, http.MethodPost, path, map[string]string{"body": body}, &result); err != nil {
		return "", err
	}
	return result.HTMLURL, nil
}

// parseOwnerRepo splits "owner/repo" into owner and repo.
// Returns an error if the format is invalid.
func parseOwnerRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be in owner/repo format, got %q", s)
	}
	return parts[0], parts[1], nil
}
