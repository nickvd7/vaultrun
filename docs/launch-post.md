# Launch / LinkedIn draft — VaultRun v0.2.x

Copy-paste ready. Adjust tone; do **not** paste internal pricing into social posts.

---

## LinkedIn (short)

VaultRun is open: a self-hosted secure runtime for AI agents.

Agents get isolated Docker sandboxes on *your* infrastructure — execute code, query DBs, call cloud APIs — without a SaaS runner or product telemetry.

What’s in the Apache 2.0 core:
• REST API + Next.js dashboard
• 53-tool MCP server (stdio + HTTP)
• Go & Python SDKs (`pip install vaultrun-sdk`)
• GitHub CI runner that executes PR tests in a sandbox

Enterprise SSO (OIDC + SAML) is a commercial overlay for teams that already have Okta / Azure AD / similar. Evaluate free for devops; production via license.

Start: https://vaultrun.dev
Source: https://github.com/nickvd7/vaultrun
Use cases: https://vaultrun.dev/use-cases.html
Talk: https://vaultrun.dev/#contact

#AI #MCP #SelfHosted #DevTools #OpenSource

---

## LinkedIn (longer / founder tone)

Most “AI agent sandboxes” want your workloads in their cloud.

We built the opposite.

**VaultRun** — self-hosted secure runtime for AI agents. One container per session, network off by default, HMAC-signed audit trail, MCP-native (53 tools). Your Docker, your Postgres, your network.

v0.2 ships open-core (Apache 2.0) plus a clear path to **VaultRun Enterprise** for SSO — so dashboard users don’t share the master API key.

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
VaultRun: Docker isolation + MCP (53 tools) + audit trail.
https://vaultrun.dev · https://github.com/nickvd7/vaultrun
