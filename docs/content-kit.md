# VaultRun content kit

Ready-to-paste copy for launches, sales, and social. Pair with [`site/brand/`](../site/brand/) assets and [`launch-post.md`](launch-post.md).

**Rule:** no fake customers, star counts, or “used by …” claims until real.

---

## Elevator pitches

**8 seconds**  
VaultRun: self-hosted sandboxes so AI agents can run code on *your* infra — with an audit trail.

**30 seconds**  
Most agent sandboxes push workloads into someone else’s cloud. VaultRun is the opposite: isolated Docker sessions on your servers, a 53-tool MCP server, Go/Python SDKs, and an HMAC-signed audit log. Apache 2.0 core; Enterprise SSO when you already have an IdP.

**60 seconds (security / platform)**  
When agents need to execute code, query databases, or touch cloud APIs, the blast radius matters. VaultRun gives each session its own container — network off by default, exec API only, path checks, signed audit events. Wire Claude or any MCP client over stdio or HTTP. Evaluate Enterprise OIDC/SAML free for dev; production is a commercial license. Start at vaultrun.dev.

---

## One-liners (swap by channel)

| Channel | Line |
|---------|------|
| GitHub about | Self-hosted secure runtime for AI agents — Docker sandboxes, MCP, audit trail |
| LinkedIn headline add-on | Building VaultRun — self-hosted agent runtime |
| Email signature | VaultRun — self-hosted secure runtime for AI agents · vaultrun.dev |
| HN title | Show HN: VaultRun – self-hosted Docker sandboxes + MCP for AI agents |
| Discord / Slack | VaultRun = agent tools in isolated Docker on your box (MCP + audit) |

---

## Feature bullets (pick 4)

- Isolated Docker sandbox per session  
- 53+ MCP tools (stdio + HTTP)  
- HMAC-signed audit trail  
- Network disabled by default  
- Go + Python SDKs  
- Self-hosted — no product telemetry  
- Enterprise: OIDC + SAML SSO (commercial)

---

## Social captions (with assets)

### Link post (use `og.png` as preview / attach `social-square.png`)

Agents need a blast radius — not another SaaS runner.

VaultRun runs tool calls in isolated Docker sandboxes on your infrastructure. MCP-native. Signed audit trail. Apache 2.0 core.

→ https://vaultrun.dev  
→ https://github.com/nickvd7/vaultrun

### Short X / Mastodon

Self-hosted sandboxes for AI agents.  
Docker isolation · MCP · audit trail · your infra.  
vaultrun.dev

### Flowd companion

Local patterns (Flowd) + isolated execution (VaultRun).  
Observe → suggest → approve locally → run risky work in a container.  
https://vaultrun.dev/flowd.html

---

## Email signature (plain)

```
Nick van Dort
VaultRun — self-hosted secure runtime for AI agents
https://vaultrun.dev  ·  https://github.com/nickvd7/vaultrun
Enterprise / SSO: mail@030.dev
```

HTML variant: use `site/brand/badge.svg` or a 20×20 `favicon-32.png` + links above.

---

## Sales one-pager talking points

Use with [`site/enterprise.html`](../site/enterprise.html) and [`site/use-cases.html`](../site/use-cases.html).

1. **Problem** — Agents need tools; shared VMs and SaaS sandboxes scare security.  
2. **Product** — Self-hosted API + sandboxes + MCP + audit.  
3. **Proof** — Repo, docs, security model, local `GET /api/v1/audit` (not logos).  
4. **Open core** — Apache 2.0 for runtime/MCP/SDKs.  
5. **Enterprise** — OIDC/SAML + org RBAC when the dashboard can’t share a master key.  
6. **Ask** — Evaluation access or 30-min call via vaultrun.dev/#contact.

---

## Demo video end card

Use `site/brand/video-endcard.png` (1920×1080). VO:

> vaultrun.dev · github.com/nickvd7/vaultrun · mail@030.dev

Full shot list: [`demo-video-script.md`](demo-video-script.md).

---

## Boilerplate (press / partner)

VaultRun is an open-core, self-hosted secure runtime for AI agents. It executes agent tool calls inside isolated Docker sandboxes on the customer’s infrastructure, exposes a Model Context Protocol (MCP) server, and records an HMAC-signed audit trail. The Apache 2.0 core includes the API, CLI, dashboard, CI runner, and SDKs. VaultRun Enterprise adds OIDC and SAML single sign-on for organizations with an existing identity provider.

---

## Asset checklist before posting

- [ ] Correct logo variant (dark vs light)  
- [ ] `og.png` or `social-square.png` attached / linked  
- [ ] Link to vaultrun.dev (and GitHub if technical audience)  
- [ ] No invented social proof  
- [ ] Enterprise vs open core not conflated  
