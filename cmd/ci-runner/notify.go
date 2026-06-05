// Slack and Microsoft Teams notifications for VaultRun CI results.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// notifier is the common interface for notification backends.
type notifier interface {
	Notify(ctx context.Context, pr prRun, steps []stepResult, pass bool) error
}

// sendNotifications dispatches to every configured notifier. Failures are
// logged and never propagate — a broken webhook must not break CI results.
func sendNotifications(ctx context.Context, cfg *config, pr prRun, steps []stepResult, pass bool) {
	if !pass || cfg.notifyOnSuccess {
		for _, n := range cfg.notifiers() {
			if err := n.Notify(ctx, pr, steps, pass); err != nil {
				// Use slog directly; caller's logger isn't threaded here.
				fmt.Printf("ci: notification error: %v\n", err)
			}
		}
	}
}

// notifiers builds the list of active notifiers from config.
func (c *config) notifiers() []notifier {
	var ns []notifier
	if c.slackWebhookURL != "" {
		ns = append(ns, &slackNotifier{url: c.slackWebhookURL, client: &http.Client{Timeout: 10 * time.Second}})
	}
	if c.teamsWebhookURL != "" {
		ns = append(ns, &teamsNotifier{url: c.teamsWebhookURL, client: &http.Client{Timeout: 10 * time.Second}})
	}
	return ns
}

// postWebhook marshals payload and POSTs it to url.
func postWebhook(ctx context.Context, client *http.Client, url string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// ── Slack ─────────────────────────────────────────────────────────────────────

type slackNotifier struct {
	url    string
	client *http.Client
}

func (s *slackNotifier) Notify(ctx context.Context, pr prRun, steps []stepResult, pass bool) error {
	icon, statusText := "✅", "passed"
	if !pass {
		icon, statusText = "❌", "failed"
	}
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.owner, pr.repo, pr.number)

	var stepLines []string
	for _, st := range steps {
		stepIcon := "✅"
		if !st.passed {
			stepIcon = "❌"
		}
		line := stepIcon + " " + st.name
		if st.duration > 0 {
			line += fmt.Sprintf(" _(%.1fs)_", st.duration.Seconds())
		}
		stepLines = append(stepLines, line)
	}

	payload := map[string]any{
		// Fallback text shown in desktop/mobile notifications and channel previews.
		"text": fmt.Sprintf("%s VaultRun CI %s — <%s|%s/%s #%d> (`%s`)",
			icon, statusText, prURL, pr.owner, pr.repo, pr.number, pr.branch),
		"blocks": []map[string]any{
			{
				"type": "header",
				"text": map[string]any{
					"type":  "plain_text",
					"text":  fmt.Sprintf("%s VaultRun CI — %s", icon, statusText),
					"emoji": true,
				},
			},
			{
				"type": "section",
				"fields": []map[string]string{
					{"type": "mrkdwn", "text": fmt.Sprintf("*Repo:*\n<%s|%s/%s #%d>", prURL, pr.owner, pr.repo, pr.number)},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Branch:*\n`%s`", pr.branch)},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Commit:*\n`%s`", pr.sha[:8])},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Triggered by:*\n%s", pr.sender)},
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": strings.Join(stepLines, "\n"),
				},
			},
			{"type": "divider"},
			{
				"type": "context",
				"elements": []map[string]string{
					{"type": "mrkdwn", "text": "Powered by *VaultRun*"},
				},
			},
		},
	}
	return postWebhook(ctx, s.client, s.url, payload)
}

// ── Microsoft Teams ───────────────────────────────────────────────────────────

type teamsNotifier struct {
	url    string
	client *http.Client
}

// Teams Workflows webhooks expect the "message" envelope with an Adaptive Card.
func (t *teamsNotifier) Notify(ctx context.Context, pr prRun, steps []stepResult, pass bool) error {
	icon, statusText, color := "✅", "Passed", "good"
	if !pass {
		icon, statusText, color = "❌", "Failed", "attention"
	}
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.owner, pr.repo, pr.number)

	// Build the step FactSet entries.
	var stepFacts []map[string]string
	for _, st := range steps {
		stepIcon := "✅"
		if !st.passed {
			stepIcon = "❌"
		}
		val := stepIcon
		if st.duration > 0 {
			val += fmt.Sprintf(" (%.1fs)", st.duration.Seconds())
		}
		stepFacts = append(stepFacts, map[string]string{"title": st.name, "value": val})
	}

	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body": []map[string]any{
			{
				"type":   "TextBlock",
				"text":   fmt.Sprintf("%s VaultRun CI — %s", icon, statusText),
				"weight": "Bolder",
				"size":   "Medium",
				"color":  color,
				"wrap":   true,
			},
			{
				"type": "FactSet",
				"facts": []map[string]string{
					{"title": "Repo", "value": fmt.Sprintf("%s/%s #%d", pr.owner, pr.repo, pr.number)},
					{"title": "Branch", "value": pr.branch},
					{"title": "Commit", "value": pr.sha[:8]},
					{"title": "Triggered by", "value": pr.sender},
				},
			},
			{
				"type":      "TextBlock",
				"text":      "Steps",
				"weight":    "Bolder",
				"spacing":   "Medium",
				"separator": true,
			},
			{
				"type":  "FactSet",
				"facts": stepFacts,
			},
		},
		"actions": []map[string]string{
			{"type": "Action.OpenUrl", "title": "View Pull Request", "url": prURL},
		},
	}

	payload := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			},
		},
	}
	return postWebhook(ctx, t.client, t.url, payload)
}
