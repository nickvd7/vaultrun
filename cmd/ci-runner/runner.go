package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// prRun holds metadata about a pull request CI run.
type prRun struct {
	owner  string
	repo   string
	number int
	sha    string
	branch string
	sender string
}

// stepResult captures the outcome of one CI step.
type stepResult struct {
	name     string
	passed   bool
	duration time.Duration
	output   string
}

const statusContext = "vaultrun-ci"

// runCI orchestrates the full CI pipeline for a pull request:
//  1. Post "pending" commit status
//  2. Spin up a VaultRun sandbox
//  3. Clone the repo (with token)
//  4. Run each configured test command sequentially
//  5. Delete the session
//  6. Post the result as a PR comment + final commit status
func runCI(ctx context.Context, cfg *config, pr prRun) {
	log := slog.With("repo", pr.owner+"/"+pr.repo, "pr", pr.number, "sha", pr.sha[:8])
	log.Info("ci: starting run", "branch", pr.branch, "sender", pr.sender)

	gh := newGithubClient(cfg.githubToken)
	vr := newVRClient(cfg.vrBaseURL, cfg.vrAPIKey)

	// Signal pending immediately so the PR shows a spinner.
	if err := gh.SetCommitStatus(ctx, pr.owner, pr.repo, pr.sha,
		"pending", "Running tests in VaultRun sandbox…", statusContext); err != nil {
		log.Warn("ci: could not set pending status", "err", err)
	}

	results, overallPass := executeSteps(ctx, cfg, vr, gh, pr, log)
	postResults(ctx, cfg, gh, pr, results, overallPass, log)
}

// executeSteps runs the clone + all test commands, returning per-step results.
func executeSteps(ctx context.Context, cfg *config, vr *vrClient, gh *githubClient, pr prRun, log *slog.Logger) ([]stepResult, bool) {
	// Create sandbox with network so git clone works.
	sess, err := vr.CreateSession(ctx, cfg.dockerImage, true, cfg.maxRunSeconds)
	if err != nil {
		log.Error("ci: create session failed", "err", err)
		return []stepResult{{name: "Create sandbox", passed: false, output: err.Error()}}, false
	}
	defer vr.DeleteSession(ctx, sess.ID)
	log.Info("ci: session created", "session_id", sess.ID)

	var steps []stepResult
	allPass := true

	// ── Step 1: clone ────────────────────────────────────────────────────────
	cloneStart := time.Now()
	cloneResult, clonePass := cloneRepo(ctx, cfg, vr, sess.ID, pr, log)
	steps = append(steps, stepResult{
		name:     fmt.Sprintf("Clone `%s/%s@%s`", pr.owner, pr.repo, pr.branch),
		passed:   clonePass,
		duration: time.Since(cloneStart),
		output:   cloneResult,
	})
	if !clonePass {
		return steps, false
	}

	// ── Steps 2+: test commands ───────────────────────────────────────────────
	for _, cmd := range cfg.testCommands {
		if !allPass {
			steps = append(steps, stepResult{
				name:   fmt.Sprintf("`%s`", strings.Join(cmd, " ")),
				passed: false,
				output: "skipped — previous step failed",
			})
			continue
		}

		start := time.Now()
		res, err := vr.RunCommand(ctx, sess.ID, cmd, nil, "/workspace/repo")
		dur := time.Since(start)
		name := fmt.Sprintf("`%s`", strings.Join(cmd, " "))

		if err != nil {
			steps = append(steps, stepResult{name: name, passed: false, duration: dur, output: err.Error()})
			allPass = false
			continue
		}

		output := runOutput(res)
		passed := res.ExitCode != nil && *res.ExitCode == 0
		if !passed {
			allPass = false
			code := -1
			if res.ExitCode != nil {
				code = *res.ExitCode
			}
			if output == "" {
				output = fmt.Sprintf("exit code %d", code)
			}
		}
		steps = append(steps, stepResult{name: name, passed: passed, duration: dur, output: output})
		log.Info("ci: step done", "cmd", cmd[0], "passed", passed, "duration_ms", dur.Milliseconds())
	}

	return steps, allPass
}

// cloneRepo clones the PR branch into /workspace/repo using http.extraheader
// so the token never appears in a remote URL.
func cloneRepo(ctx context.Context, cfg *config, vr *vrClient, sessionID string, pr prRun, log *slog.Logger) (string, bool) {
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", pr.owner, pr.repo)
	env := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_CONFIG_COUNT":    "1",
		// Token via HTTP extraheader — keeps the token out of any URL or log key.
		"GIT_CONFIG_KEY_0":   "http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0": "Authorization: Bearer " + cfg.githubToken,
	}
	res, err := vr.RunCommand(ctx, sessionID,
		[]string{"git", "clone", "--branch", pr.branch, "--depth", "1", "--", cloneURL, "/workspace/repo"},
		env, "/")
	if err != nil {
		log.Error("ci: clone failed (API error)", "err", err)
		return err.Error(), false
	}
	output := scrubToken(runOutput(res), cfg.githubToken)
	passed := res.ExitCode != nil && *res.ExitCode == 0
	return output, passed
}

// scrubToken replaces all occurrences of token with [REDACTED].
func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}

// postResults writes the PR comment and final commit status.
func postResults(ctx context.Context, cfg *config, gh *githubClient, pr prRun, steps []stepResult, pass bool, log *slog.Logger) {
	comment := buildComment(pr, steps, pass)
	if err := gh.PostComment(ctx, pr.owner, pr.repo, pr.number, comment); err != nil {
		log.Error("ci: post comment failed", "err", err)
	}

	state := "success"
	desc := fmt.Sprintf("All %d steps passed", len(steps))
	if !pass {
		state = "failure"
		failed := 0
		for _, s := range steps {
			if !s.passed {
				failed++
			}
		}
		desc = fmt.Sprintf("%d/%d steps failed", failed, len(steps))
	}
	if err := gh.SetCommitStatus(ctx, pr.owner, pr.repo, pr.sha, state, desc, statusContext); err != nil {
		log.Error("ci: set final status failed", "err", err)
	}
	log.Info("ci: run complete", "passed", pass, "steps", len(steps))
}

// buildComment generates the Markdown PR comment.
func buildComment(pr prRun, steps []stepResult, pass bool) string {
	var sb strings.Builder

	icon := "✅"
	headline := "All checks passed"
	if !pass {
		icon = "❌"
		headline = "Some checks failed"
	}

	fmt.Fprintf(&sb, "## %s VaultRun CI — %s\n\n", icon, headline)
	fmt.Fprintf(&sb, "**Repo:** `%s/%s` · **Branch:** `%s` · **Commit:** `%s`\n\n",
		pr.owner, pr.repo, pr.branch, pr.sha[:8])

	// Summary table.
	sb.WriteString("| Step | Result | Duration |\n")
	sb.WriteString("|---|:---:|---|\n")
	var totalDur time.Duration
	for _, s := range steps {
		result := "✅ Pass"
		if !s.passed {
			result = "❌ Fail"
		}
		dur := ""
		if s.duration > 0 {
			dur = fmt.Sprintf("%.1fs", s.duration.Seconds())
		}
		fmt.Fprintf(&sb, "| %s | %s | %s |\n", s.name, result, dur)
		totalDur += s.duration
	}
	if totalDur > 0 {
		fmt.Fprintf(&sb, "\n**Total:** %.1fs\n", totalDur.Seconds())
	}

	// Collapsible output sections for failed (or all if verbose) steps.
	for _, s := range steps {
		if s.output == "" {
			continue
		}
		// Always show output for failed steps; collapse output for passing steps.
		if !s.passed {
			fmt.Fprintf(&sb, "\n### Output: %s\n\n```\n%s\n```\n", s.name, truncate(s.output, 8000))
		} else {
			fmt.Fprintf(&sb, "\n<details>\n<summary>Output: %s</summary>\n\n```\n%s\n```\n\n</details>\n",
				s.name, truncate(s.output, 4000))
		}
	}

	sb.WriteString("\n---\n*Powered by [VaultRun](https://github.com/nickvd7/vaultrun)*")
	return sb.String()
}

// truncate shortens output to at most n bytes, adding a notice if cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n… (truncated, %d bytes omitted)", len(s)-n)
}
