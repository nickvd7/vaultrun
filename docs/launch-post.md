# Launch / LinkedIn draft — VaultRun

Copy-paste ready. Adjust tone; do **not** paste internal pricing into social posts.

Company **Page** first post, uploads, and settings: [`linkedin-page.md`](linkedin-page.md). Use this file for the **founder (personal) profile** and HN.

Public MCP wording stays **53+ tools** (matches the site). Do not switch to “61” until the homepage is updated in one pass.

---

## LinkedIn (short)

VaultRun is open: a self-hosted secure runtime for AI agents.

Agents get isolated Docker sandboxes on *your* infrastructure — execute code, query DBs, call cloud APIs — without a SaaS runner or product telemetry.

What’s in the Apache 2.0 core:
• REST API + Next.js dashboard
• 53+ MCP tools (stdio + HTTP)
• Go & Python SDKs (`pip install vaultrun-sdk`)
• GitHub CI runner that executes PR tests in a sandbox
• Local-first workflow assets (missions, verify, memory)

Enterprise SSO (OIDC + SAML) is a commercial overlay for teams that already have Okta / Azure AD / similar. Evaluate free for devops; production via license.

Start: https://vaultrun.dev
Source: https://github.com/nickvd7/vaultrun
Use cases: https://vaultrun.dev/use-cases.html
Talk: https://vaultrun.dev/#contact

#AI #MCP #SelfHosted #DevTools #OpenSource

Company Page launch (attach `linkedin-first-post.png`, put the four URLs in the first comment): [`linkedin-page.md`](linkedin-page.md).

---

## LinkedIn (longer / founder tone)

Most “AI agent sandboxes” want your workloads in their cloud.

We built the opposite.

**VaultRun** — self-hosted secure runtime for AI agents. One container per session, network off by default, HMAC-signed audit trail, MCP-native (53+ tools). Your Docker, your Postgres, your network. Successful runs become workflow assets you keep.

Open-core (Apache 2.0) plus a clear path to **VaultRun Enterprise** for SSO — so dashboard users don’t share the master API key.

If you’re a platform or security team putting LLM tools into prod: clone it, break it, mail us if you need IdP federation.

https://vaultrun.dev
https://github.com/nickvd7/vaultrun

---

## GitHub Discussions / Release comment blurb

```
VaultRun v0.2.1 — website + Enterprise acquisition clarity + Python SDK metadata.

- Site: https://vaultrun.dev
- Enterprise: https://vaultrun.dev/#enterprise
- Use cases: https://vaultrun.dev/use-cases.html
- PyPI: pip install vaultrun-sdk
```

---

## LinkedIn — VaultRun + Flowd (week 2)

Local file workflows and agent sandboxes solve different problems.

**Flowd** watches your machine, detects repeated rename/move patterns, and suggests automations you approve in the terminal.

**VaultRun** gives AI agents isolated Docker sandboxes on *your* infra — with MCP tools and an audit trail.

Together: observe → suggest → approve locally, then run risky work in a container.

Flowd: https://flowd.net
VaultRun: https://vaultrun.dev/flowd.html
#LocalFirst #MCP #AIAgents #DevTools

---

## Optional tweet / X

Self-hosted sandboxes for AI agents — not another SaaS runner.
VaultRun: Docker isolation + MCP (53+ tools) + audit trail.
https://vaultrun.dev · https://github.com/nickvd7/vaultrun

---

## LinkedIn comment reply — HMAC vs hallucinated tool args

Reply **from Nick’s personal profile**, not the Page. Keep it under the comment; don’t start a new post.

```
Yes — the HMAC trail records the actual inputs, not just that a call happened.

On a sandbox run we write command.started with the command + args into metadata JSON, then HMAC-SHA256 over that payload (id, timestamp, actor, action, session/run ids, metadata). So a model that invents rm -rf / or a weird flag still shows up as those exact args. Credential-looking flags (--token, --password, --api-key, …) are replaced with *** before the row is signed. Secret-broker values never land in audit metadata.

What it does not capture: full stdout, or a “the model meant X” reconstruction. command.finished is exit status + duration.

The container is still the blast radius. The log is how you prove what crossed it. GET /api/v1/audit on your own instance — or the source in internal/audit + internal/runner.
```

Shorter variant if the thread is already long:

```
Actual inputs. command.started stores command + args in the signed metadata (credential-looking flags redacted). Not just “a call happened,” and not full stdout. Container = blast radius; HMAC log = what crossed it. GET /api/v1/audit on your box.
```

Indie Hackers launch post + product listing: [`indiehackers.md`](indiehackers.md).
