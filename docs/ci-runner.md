# CI Runner — Setup Guide

The CI runner is a self-hosted GitHub webhook receiver that runs PR test suites inside
VaultRun sandboxes and posts results back to the pull request as a comment and commit status.

## How it works

```
GitHub PR event (opened / synchronize / reopened)
         │
         │  POST /webhook  (HMAC-SHA256 verified)
         ▼
  ci-runner process
         │
         ├─ 1. Set commit status: "pending"
         ├─ 2. Create VaultRun sandbox
         ├─ 3. git clone --depth 1 (token via http.extraheader)
         ├─ 4. Run each CI_TEST_COMMANDS command sequentially
         ├─ 5. Delete sandbox
         ├─ 6. Post PR comment (Markdown table + output)
         ├─ 7. Set commit status: "success" or "failure"
         └─ 8. Send Slack / Teams notification (optional)
```

## Prerequisites

- A running VaultRun instance (see main README quickstart)
- A GitHub repository with webhook access
- A GitHub PAT with `repo` and `write:discussion` scopes

## Build

```bash
go build -o ci-runner ./cmd/ci-runner/
```

Or add to your PATH via `make build` (outputs to `bin/`).

## Configuration

All configuration is via environment variables.

### Required

| Variable | Description |
|---|---|
| `GITHUB_TOKEN` | GitHub PAT with `repo` + `write:discussion` scopes |
| `GITHUB_WEBHOOK_SECRET` | Secret set in the GitHub webhook settings |
| `VAULTRUN_BASE_URL` | Base URL of the VaultRun API |
| `VAULTRUN_API_KEY` | VaultRun API key |

### Optional

| Variable | Default | Description |
|---|---|---|
| `PORT` | `:8080` | HTTP listen address |
| `CI_DOCKER_IMAGE` | `ubuntu:22.04` | Docker image for the sandbox |
| `CI_TEST_COMMANDS` | `[["make","test"]]` | JSON array of command arrays to run after cloning |
| `CI_MAX_RUN_SECONDS` | `1800` | Maximum seconds for a single CI run (30 min) |
| `SLACK_WEBHOOK_URL` | — | Slack Incoming Webhook URL for notifications |
| `TEAMS_WEBHOOK_URL` | — | Microsoft Teams Workflows webhook URL for notifications |
| `NOTIFY_ON_SUCCESS` | `true` | Set `false` to suppress notifications on green runs |

## GitHub webhook setup

1. Go to your repository → **Settings** → **Webhooks** → **Add webhook**
2. **Payload URL:** `https://your-ci-runner.example.com/webhook`
3. **Content type:** `application/json`
4. **Secret:** a random string (use the same value for `GITHUB_WEBHOOK_SECRET`)
5. **Events:** select **Pull requests** only (or "Let me select individual events")
6. Click **Add webhook**

## Running

### Minimal (no notifications)

```bash
GITHUB_TOKEN=ghp_...               \
GITHUB_WEBHOOK_SECRET=your-secret  \
VAULTRUN_BASE_URL=http://vaultrun  \
VAULTRUN_API_KEY=vr_...            \
./ci-runner
```

### With Slack + Teams notifications

```bash
GITHUB_TOKEN=ghp_...               \
GITHUB_WEBHOOK_SECRET=your-secret  \
VAULTRUN_BASE_URL=http://vaultrun  \
VAULTRUN_API_KEY=vr_...            \
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../... \
TEAMS_WEBHOOK_URL=https://...webhook.office.com/webhookb2/...    \
NOTIFY_ON_SUCCESS=false            \
./ci-runner
```

### Custom test commands

```bash
CI_TEST_COMMANDS='[["go","test","./..."],["go","vet","./..."]]' \
CI_DOCKER_IMAGE=golang:1.23-slim \
...
./ci-runner
```

`CI_TEST_COMMANDS` is a JSON array of arrays. Each inner array is a command + arguments.
Commands run sequentially in the cloned repo at `/workspace/repo`. If a step fails,
remaining steps are marked as skipped.

## What the PR comment looks like

```markdown
## ✅ VaultRun CI — All checks passed

**Repo:** `acme/backend` · **Branch:** `fix/auth-bug` · **Commit:** `deadbeef`

| Step | Result | Duration |
|---|:---:|---|
| Clone `acme/backend@fix/auth-bug` | ✅ Pass | 3.0s |
| `make test` | ✅ Pass | 12.0s |

**Total:** 15.0s

---
*Powered by [VaultRun](https://github.com/nickvd7/vaultrun)*
```

On failure, the output of the failing step is shown in full (up to 8 000 chars).
Output for passing steps is in a collapsible `<details>` block.

## Notifications

### Slack

Sends a Block Kit message with:
- Header with ✅/❌ and repo name
- 4-field section: repo, branch, commit SHA, PR author
- List of steps with pass/fail and duration
- Footer with PR link

Set `NOTIFY_ON_SUCCESS=false` to only send on failures.

### Microsoft Teams

Sends an Adaptive Card 1.4 inside a Workflows webhook envelope with:
- Color-coded card (green on pass, red on failure)
- FactSet with PR metadata
- FactSet with step results
- "View Pull Request" action button

### Getting webhook URLs

**Slack:** Create an [Incoming Webhook](https://api.slack.com/messaging/webhooks) app
for your workspace and copy the URL.

**Teams:** In a Teams channel, add a **Workflows** connector, choose
"Post to a channel when a webhook request is received", and copy the generated URL.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `POST` | `/webhook` | GitHub webhook receiver |
| `GET` | `/healthz` | Health check — returns `{"ok":"true"}` |

## Graceful shutdown

On `SIGINT` or `SIGTERM`, the runner stops accepting new webhooks and waits up to
5 minutes for in-flight CI runs to complete before exiting. This prevents partial
results when deploying a new version.

## Security

- **HMAC-SHA256 validation** — every incoming webhook is verified against
  `GITHUB_WEBHOOK_SECRET` using constant-time comparison. Requests with missing or
  invalid signatures return `401`.
- **Token-safe clone** — the GitHub token is passed via `GIT_CONFIG_KEY_0 =
  http.https://github.com/.extraheader`. It never appears in the git remote URL,
  process list, or log output.
- **Action filtering** — only `opened`, `synchronize`, and `reopened` PR actions
  trigger a run. All other events are ignored.
- **Async execution** — each PR run is launched in a separate goroutine. The webhook
  endpoint returns `202 Accepted` immediately, so GitHub's 10-second timeout is
  never exceeded.

## Systemd unit (example)

```ini
[Unit]
Description=VaultRun CI Runner
After=network.target

[Service]
Type=simple
User=ci-runner
EnvironmentFile=/etc/vaultrun/ci-runner.env
ExecStart=/usr/local/bin/ci-runner
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

`/etc/vaultrun/ci-runner.env`:
```
GITHUB_TOKEN=ghp_...
GITHUB_WEBHOOK_SECRET=...
VAULTRUN_BASE_URL=http://localhost:8080
VAULTRUN_API_KEY=vr_...
SLACK_WEBHOOK_URL=https://...
NOTIFY_ON_SUCCESS=false
```
