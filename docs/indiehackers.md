# Indie Hackers — VaultRun

Status: **2026-08-14** · Pair with [`launch-post.md`](launch-post.md) (LinkedIn / HN).

IH rewards a founder story, not a press release. No fake revenue, users, or “used by.” Apache 2.0 core vs Enterprise SSO: keep them separate. Public MCP wording stays **53+**.

---

## How to add it (order)

1. **Account** — indiehackers.com, log in (Twitter/Google is fine). Fill the bio: `Building VaultRun — self-hosted runtime for AI agents`. Link vaultrun.dev.
2. **Warm up (if the account is new)** — comment on 3–5 posts in Building / Launch before you drop your own. Cold launch-spam gets ignored.
3. **Product listing first** — avatar menu → **Products** → add product. This is the durable directory page; the discussion post is the reach.
4. **Discussion post** — **Start a discussion** → group **Building** (tech/self-hosted) or **Launch** (announcement day). One group, not five.
5. **Milestone** — after the post: product page → **Add milestone** → Launch. 2–3 sentences, no extra links.
6. **First 8 hours** — reply to every comment. Same as LinkedIn: early replies are the ranking signal.

Do **not** dump this the same hour as Show HN. IH in the morning Europe / US-East overlap (Tue–Thu); HN a different morning.

### Product listing (paste)

| Field | Value |
|-------|--------|
| Name | VaultRun |
| Tagline | Self-hosted secure runtime for AI agents |
| Website | https://vaultrun.dev |
| Topics | AI, developer tools, security, open source, self-hosted |
| Logo | `site/brand/linkedin-logo.png` or `mark.png` |

**Description** (listing, not the post):

```
I got tired of “just let the agent run in our cloud.” If an LLM is going to exec code, query a DB, or call AWS, I want that on my Docker host, with a log I can show security — not a SaaS sandbox and a hope.

VaultRun is an open-core runtime: one isolated container per session, network off by default, 53+ MCP tools (stdio or HTTP), HMAC-signed audit trail, Go/Python SDKs. Apache 2.0 for the core. Enterprise SSO (OIDC/SAML) is a separate commercial overlay if you already have Okta / Azure AD.

It’s early. Clone it, break it, tell me what’s missing.
```

---

## Discussion post (paste)

**Group:** Building  
**Title:** I didn’t want AI agents exec’ing code in someone else’s cloud, so I built a self-hosted sandbox

```
Most “agent sandboxes” I looked at wanted the workload in their cloud. That’s a non-starter if the agent might touch customer data, prod credentials, or just rm something dumb because it hallucinated a flag.

So I built VaultRun: a self-hosted runtime. One Docker container per session, network off unless you allowlist hosts, exec API (no shell string), HMAC-signed audit log of the actual command + args — not merely “a tool was called.” MCP-native (53+ tools, stdio or HTTP). Apache 2.0 core. No product telemetry.

What I kept bumping into while building:

1. Isolation is the easy slide. The argument that actually matters to a security person is: can I prove what the model sent, after the fact, on infra I control?
2. Open-core vs “enterprise-grade AI platform.” The core is the API, sandbox, MCP, dashboard, SDKs. SSO (OIDC/SAML) is a commercial overlay for teams that already have an IdP — dashboard users shouldn’t share a master API key.
3. Local-first: if a sequence of tool calls worked, I want that as an asset on my disk (missions, verify checkpoints, sandbox memory) — not a prompt lost in a vendor UI.

It’s early open source. I’m not going to invent user counts.

If you put LLM tools near prod: what would you need to trust a self-hosted runtime? Policy? Replay? Something else?

https://vaultrun.dev
https://github.com/nickvd7/vaultrun
```

---

## What not to do on IH

- Title starting with “Introducing” / “Show IH:” (that’s HN)
- More than two links in the body
- Pricing, eval discounts, or “starting from”
- Posting the same block in Growth + Marketing + Launch the same day
- Ignoring comments overnight

Follow-up later (separate post, after you have a real number): demo video, first enterprise conversation, or “what the HMAC comment thread taught me” — not another launch paste.
