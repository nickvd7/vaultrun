# VaultRun demo video script (2–3 minutes)

VaultRun cannot render a finished MP4 from this repo CI. Use this script in Loom, OBS, Screen Studio, or CapCut. Target length: **~150 seconds**.

## Goal

Viewer understands in under three minutes: what VaultRun is, that it runs on *their* infra, how an agent session works, and how to get Enterprise SSO.

## Shot list

| # | Duration | Visual | Voiceover |
|---|---|---|---|
| 1 | 0:00–0:15 | vaultrun.dev hero / ASCII logo | “VaultRun is a self-hosted secure runtime for AI agents — isolated Docker sandboxes on your infrastructure. No SaaS. No telemetry.” |
| 2 | 0:15–0:35 | Terminal: `make up` → health curl → dashboard | “Clone the open-core repo, start the stack, bootstrap an API key. The API and dashboard run where you already run Postgres, Redis, and Docker.” |
| 3 | 0:35–1:10 | Dashboard or CLI: create session → upload script → run | “Each session is its own container. Agents upload files and run commands through the exec API — not a shell on the host. Network is off by default.” |
| 4 | 1:10–1:40 | MCP config snippet (Claude or HTTP) | “Fifty-three MCP tools over stdio for Claude, or HTTP for OpenAI, OpenRouter, and custom agents — sandboxes, databases, optional AWS, CI helpers.” |
| 5 | 1:40–2:05 | Audit log screen / security bullets | “Every action lands in an HMAC-signed audit trail. Path traversal checks, non-root containers, capabilities dropped.” |
| 6 | 2:05–2:35 | Enterprise page / IdP logos text | “Need Okta, Azure AD, or SAML for the dashboard? That’s VaultRun Enterprise — evaluate free for dev and test; production is a commercial license.” |
| 7 | 2:35–2:50 | Contact / schedule form | “Propose call times on vaultrun.dev — requests go to mail@030.dev. Or start with `pip install vaultrun-sdk` against your own API.” |
| 8 | 2:50–3:00 | End card: github + vaultrun.dev | “vaultrun.dev · github.com/nickvd7/vaultrun · mail@030.dev” |

## On-screen end card (text)

```
VaultRun — self-hosted secure runtime for AI agents
https://vaultrun.dev
https://github.com/nickvd7/vaultrun
Enterprise: https://vaultrun.dev/#enterprise
```

## B-roll assets already in-repo

- Site: `site/index.html`, `site/enterprise.html`, `site/use-cases.html`
- Quickstart commands: README.md
- MCP JSON: docs/mcp.md / site MCP section
- SSO setup: docs/sso-setup.md

## After export

1. Upload to YouTube / Loom (unlisted or public).
2. Drop the URL into README and vaultrun.dev hero CTA when ready.
3. Optional: 15s LinkedIn cut from shots 1 + 3 + 6.
