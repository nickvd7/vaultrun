package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// webhookHandler receives GitHub PR webhook events.
type webhookHandler struct {
	cfg *config
}

func (h *webhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20)) // 25 MB
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if !validateSignature(h.cfg.webhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		slog.Warn("ci: webhook signature mismatch")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-Github-Event")
	if eventType != "pull_request" {
		// Acknowledge non-PR events without doing anything.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	pr, ok := parsePREvent(body)
	if !ok {
		// Ignore unrecognised or irrelevant PR actions (merged, closed, etc.)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Respond immediately so GitHub doesn't retry — CI runs in background.
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("accepted"))

	go runCI(r.Context(), h.cfg, *pr)
}

// validateSignature checks the HMAC-SHA256 GitHub webhook signature.
func validateSignature(secret, sigHeader string, body []byte) bool {
	if !strings.HasPrefix(sigHeader, "sha256=") {
		return false
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(sigHeader, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(sig, expected)
}

// prWebhookPayload is the minimal subset of the GitHub PR webhook we need.
type prWebhookPayload struct {
	Action string `json:"action"`
	Number int    `json:"number"`
	PullRequest struct {
		Head struct {
			SHA   string `json:"sha"`
			Ref   string `json:"ref"`
			Label string `json:"label"`
		} `json:"head"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"` // "owner/repo"
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// parsePREvent decodes a pull_request webhook payload. Returns nil when the
// event action is not one we act on (e.g. closed, labeled, review_requested).
func parsePREvent(body []byte) (*prRun, bool) {
	var p prWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, false
	}

	// Only run CI when code changes: opened, synchronize (new push), reopened.
	switch p.Action {
	case "opened", "synchronize", "reopened":
	default:
		return nil, false
	}

	parts := strings.SplitN(p.Repository.FullName, "/", 2)
	if len(parts) != 2 || p.PullRequest.Head.SHA == "" || p.PullRequest.Head.Ref == "" {
		slog.Warn("ci: malformed PR payload", "action", p.Action)
		return nil, false
	}

	return &prRun{
		owner:  parts[0],
		repo:   parts[1],
		number: p.Number,
		sha:    p.PullRequest.Head.SHA,
		branch: p.PullRequest.Head.Ref,
		sender: p.Sender.Login,
	}, true
}

// healthzHandler returns 200 OK with a plain-text body.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "ok")
}
