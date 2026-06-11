package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testPR() prRun {
	return prRun{
		owner:  "acme",
		repo:   "app",
		number: 42,
		sha:    "deadbeef1234abcd",
		branch: "fix/bug-123",
		sender: "alice",
	}
}

func testSteps(pass bool) []stepResult {
	return []stepResult{
		{name: "Clone `acme/app@fix/bug-123`", passed: true, duration: 3 * time.Second},
		{name: "`make test`", passed: pass, duration: 12 * time.Second, output: func() string {
			if pass {
				return "ok  github.com/acme/app"
			}
			return "FAIL: 2 tests failed"
		}()},
	}
}

// captureWebhook spins up an httptest server that records what it receives.
func captureWebhook(t *testing.T) (url string, body func() []byte) {
	t.Helper()
	var captured []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts.URL, func() []byte { return captured }
}

// ── Slack ─────────────────────────────────────────────────────────────────────

func TestSlackNotifyPass(t *testing.T) {
	url, body := captureWebhook(t)
	n := &slackNotifier{url: url, client: http.DefaultClient}
	if err := n.Notify(context.Background(), testPR(), testSteps(true), true); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	raw := body()
	if len(raw) == 0 {
		t.Fatal("no payload sent")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, raw)
	}
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "passed") {
		t.Errorf("fallback text missing 'passed': %q", text)
	}
	if !strings.Contains(text, "acme/app") {
		t.Errorf("fallback text missing repo: %q", text)
	}
	blocks, _ := payload["blocks"].([]any)
	if len(blocks) < 3 {
		t.Errorf("expected at least 3 blocks, got %d", len(blocks))
	}
}

func TestSlackNotifyFail(t *testing.T) {
	url, body := captureWebhook(t)
	n := &slackNotifier{url: url, client: http.DefaultClient}
	if err := n.Notify(context.Background(), testPR(), testSteps(false), false); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	text, _ := func() (string, bool) {
		var p map[string]any
		json.Unmarshal(body(), &p)
		s, ok := p["text"].(string)
		return s, ok
	}()
	if !strings.Contains(text, "failed") {
		t.Errorf("fallback text missing 'failed': %q", text)
	}
	if !strings.Contains(text, "❌") {
		t.Errorf("fallback text missing ❌: %q", text)
	}
}

func TestSlackWebhookError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	n := &slackNotifier{url: ts.URL, client: http.DefaultClient}
	err := n.Notify(context.Background(), testPR(), testSteps(true), true)
	if err == nil {
		t.Error("expected error on 500, got nil")
	}
}

// ── Teams ─────────────────────────────────────────────────────────────────────

func TestTeamsNotifyPass(t *testing.T) {
	url, body := captureWebhook(t)
	n := &teamsNotifier{url: url, client: http.DefaultClient}
	if err := n.Notify(context.Background(), testPR(), testSteps(true), true); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	raw := body()
	if len(raw) == 0 {
		t.Fatal("no payload sent")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["type"] != "message" {
		t.Errorf("expected type=message, got %v", payload["type"])
	}
	attachments, _ := payload["attachments"].([]any)
	if len(attachments) == 0 {
		t.Fatal("expected at least one attachment")
	}
	att := attachments[0].(map[string]any)
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("wrong contentType: %v", att["contentType"])
	}
	card := att["content"].(map[string]any)
	if card["type"] != "AdaptiveCard" {
		t.Errorf("wrong card type: %v", card["type"])
	}
	// Verify the PR action URL is present.
	raw2 := string(raw)
	if !strings.Contains(raw2, "pull/42") {
		t.Error("PR URL missing from Teams payload")
	}
	if !strings.Contains(raw2, "Passed") {
		t.Error("status 'Passed' missing from Teams payload")
	}
}

func TestTeamsNotifyFail(t *testing.T) {
	url, body := captureWebhook(t)
	n := &teamsNotifier{url: url, client: http.DefaultClient}
	if err := n.Notify(context.Background(), testPR(), testSteps(false), false); err != nil {
		t.Fatalf("Notify error: %v", err)
	}
	raw := string(body())
	if !strings.Contains(raw, "Failed") {
		t.Error("status 'Failed' missing from Teams payload")
	}
	if !strings.Contains(raw, "attention") {
		t.Error("color 'attention' missing from Teams payload")
	}
}

func TestTeamsWebhookError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	n := &teamsNotifier{url: ts.URL, client: http.DefaultClient}
	err := n.Notify(context.Background(), testPR(), testSteps(true), true)
	if err == nil {
		t.Error("expected error on 400, got nil")
	}
}

// ── Config-driven dispatch (sendNotifications) ─────────────────────────────────

func TestSendNotificationsOnlyOnFailureWhenSuccessSuppressed(t *testing.T) {
	slackURL, slackBody := captureWebhook(t)

	cfg := &config{
		slackWebhookURL: slackURL,
		notifyOnSuccess: false, // suppress success
	}

	// Pass = true → should NOT send
	sendNotifications(context.Background(), cfg, testPR(), testSteps(true), true)
	if len(slackBody()) > 0 {
		t.Error("expected no notification on success when notifyOnSuccess=false")
	}

	// Pass = false → SHOULD send
	sendNotifications(context.Background(), cfg, testPR(), testSteps(false), false)
	if len(slackBody()) == 0 {
		t.Error("expected notification on failure")
	}
}

func TestSendNotificationsBothChannels(t *testing.T) {
	slackURL, slackBody := captureWebhook(t)
	teamsURL, teamsBody := captureWebhook(t)

	cfg := &config{
		slackWebhookURL: slackURL,
		teamsWebhookURL: teamsURL,
		notifyOnSuccess: true,
	}

	sendNotifications(context.Background(), cfg, testPR(), testSteps(true), true)

	if len(slackBody()) == 0 {
		t.Error("Slack: no payload sent")
	}
	if len(teamsBody()) == 0 {
		t.Error("Teams: no payload sent")
	}
}

func TestNoNotifiersConfigured(t *testing.T) {
	cfg := &config{notifyOnSuccess: true}
	// Should complete without panic even when no notifiers are set.
	sendNotifications(context.Background(), cfg, testPR(), testSteps(true), true)
}

func TestConfigNotifyOnSuccessDefault(t *testing.T) {
	t.Setenv("VAULTRUN_BASE_URL", "http://localhost")
	t.Setenv("VAULTRUN_API_KEY", "key")
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "sec")
	t.Setenv("NOTIFY_ON_SUCCESS", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !cfg.notifyOnSuccess {
		t.Error("notifyOnSuccess should default to true")
	}
}

func TestConfigNotifyOnSuccessFalse(t *testing.T) {
	t.Setenv("VAULTRUN_BASE_URL", "http://localhost")
	t.Setenv("VAULTRUN_API_KEY", "key")
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "sec")
	t.Setenv("NOTIFY_ON_SUCCESS", "false")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.notifyOnSuccess {
		t.Error("notifyOnSuccess should be false when NOTIFY_ON_SUCCESS=false")
	}
}
