package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const githubAPI = "https://api.github.com"

type githubClient struct {
	token      string
	httpClient *http.Client
}

func newGithubClient(token string) *githubClient {
	return &githubClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *githubClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, githubAPI+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+g.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.httpClient.Do(req)
}

func (g *githubClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := g.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var e struct{ Message string `json:"message"` }
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			return fmt.Errorf("GitHub %d: %s", resp.StatusCode, e.Message)
		}
		return fmt.Errorf("GitHub %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// PostComment posts a Markdown comment on a PR/issue.
func (g *githubClient) PostComment(ctx context.Context, owner, repo string, number int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), number)
	return g.doJSON(ctx, http.MethodPost, path, map[string]string{"body": body}, nil)
}

// SetCommitStatus sets a GitHub commit status (pending/success/failure/error).
func (g *githubClient) SetCommitStatus(ctx context.Context, owner, repo, sha, state, description, context_ string) error {
	path := fmt.Sprintf("/repos/%s/%s/statuses/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha))
	return g.doJSON(ctx, http.MethodPost, path, map[string]string{
		"state":       state,
		"description": description,
		"context":     context_,
	}, nil)
}
