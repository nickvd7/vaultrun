# LinkedIn company page kit — VaultRun

Status: **2026-08-14** · Page created (name/URL: vaultrun.dev) · fill fields + assets below before the first post.

Pair with [`site/brand/`](../site/brand/) (upload files) and [`launch-post.md`](launch-post.md) (personal-profile variant). Regenerate rasters: `python3 scripts/generate-brand-pngs.py`.

**Rule:** English on the Page (ICP is platform / security engineering). No fake customers, star counts, or pricing.

---

## What to upload

| LinkedIn field | File | Size | Notes |
|----------------|------|------|--------|
| **Logo** | [`linkedin-logo.png`](../site/brand/linkedin-logo.png) | 400×400 | Dark mark with fill — reads on light *and* dark LinkedIn UI. Do not use the wide lockup; it becomes illegible at feed size. |
| **Cover / header** | [`linkedin-cover.png`](../site/brand/linkedin-cover.png) | 4200×700 | Official upload size (renders ~1128×191). No mark on the left — LinkedIn overlays the page logo there. Max 3 MB, PNG/JPG. |
| **First post image** | [`linkedin-first-post.png`](../site/brand/linkedin-first-post.png) | 1080×1080 | Attach as a native image. Do **not** also paste a URL in the post body (algorithm throttles link posts). |
| **Link preview** (later) | [`og.png`](../site/brand/og.png) | 1200×630 | Used when someone shares vaultrun.dev. Already referenced by the site. |
| **Square posts** | [`social-square.png`](../site/brand/social-square.png) | 1080×1080 | Generic square if you need a second image. |

### Founder personal profile (Nick)

| Field | File / copy |
|-------|-------------|
| Banner | [`linkedin-profile-banner.png`](../site/brand/linkedin-profile-banner.png) — 1584×396 |
| Photo | Keep a real photo (not the mark). Pages use the mark; people use faces. |
| Headline | `Building VaultRun — self-hosted agent runtime` |
| Experience | Add **VaultRun** as current role; set it as the company Page so the logo appears on your profile. |
| Featured | https://vaultrun.dev + the first company post once it is live |
| Creator mode | On — better organic reach than a company Page |

---

## Page identity (paste)

| Field | Value |
|-------|--------|
| **Name** | `VaultRun` (not `vaultrun.dev` — that belongs in Website / custom URL) |
| **Public URL** | Keep `linkedin.com/company/vaultrun-dev` or claim `…/company/vaultrun` if still free |
| **Tagline** (≤120) | `Self-hosted secure runtime for AI agents. Docker sandboxes, MCP, signed audit trail.` |
| **Website** | `https://vaultrun.dev` |
| **Industry** | Software Development |
| **Company size** | 1 employee (or 2–10 if you prefer not to look like a solo shop) |
| **Type** | Privately held |
| **Founded** | 2026 |
| **Headquarters** | Netherlands (Utrecht if you want a city; no street address required) |
| **Language** | English |
| **Button** | **Visit website** → `https://vaultrun.dev` |

### Specialties (up to 20)

```
AI agent runtime
Docker sandboxes
Model Context Protocol (MCP)
Self-hosted infrastructure
Security and audit
Open source
Platform engineering
DevSecOps
Enterprise SSO
CI sandboxes
```

### About (≤2000 characters) — paste as-is

```
VaultRun is an open-core, self-hosted secure runtime for AI agents.

Most agent sandboxes send your workloads into someone else’s cloud. VaultRun does the opposite: each session gets its own isolated Docker container on your infrastructure — network off by default, exec API only, HMAC-signed audit trail. No product telemetry.

Apache 2.0 core:
• REST API and Next.js dashboard
• 53+ MCP tools (stdio + HTTP)
• Go and Python SDKs (pip install vaultrun-sdk)
• GitHub CI runner that executes PR tests in a sandbox
• Local-first workflow assets: missions, verify checkpoints, sandbox memory

VaultRun Enterprise adds OIDC and SAML SSO for teams that already have Okta, Azure AD, or similar — so dashboard users don’t share a master API key.

Start: https://vaultrun.dev
Source: https://github.com/nickvd7/vaultrun
Enterprise: https://vaultrun.dev/#enterprise
```

---

## First post (company Page)

Post **as the Page**, attach `linkedin-first-post.png`, no URL in the body. Put links in the **first comment**. Then reshare from Nick’s personal profile (that is where reach actually lives).

### Body

```
Agents need a blast radius — not another SaaS runner.

VaultRun is live: a self-hosted secure runtime for AI agents.

Each session gets its own isolated Docker container on your infrastructure. Network off by default. MCP-native. HMAC-signed audit trail. Apache 2.0 core. No product telemetry.

What ships today:
• One container per agent session
• 53+ MCP tools (stdio + HTTP)
• Go & Python SDKs
• An audit log you can actually show security

If you put LLM tools in production and SaaS sandboxes make your CISO nervous — clone it, break it, tell us what you need.

#AIAgents #MCP #SelfHosted #DevTools #OpenSource
```

### First comment (links live here)

```
Start → https://vaultrun.dev
Source → https://github.com/nickvd7/vaultrun
Use cases → https://vaultrun.dev/use-cases.html
Talk → https://vaultrun.dev/#contact
```

### After you post

1. Pin it (Page → Post → Pin).
2. Add it to **Featured**.
3. From Nick’s profile: **Repost with your own intro** (2–4 lines, not a naked share). Tag the Page. Example intro:

```
We built the opposite of a SaaS agent sandbox.

VaultRun runs tool calls in isolated Docker on your infra — MCP, signed audit, Apache 2.0 core.

If you’re platform or security and agents are about to touch prod, take a look.
```

Longer founder version: [`launch-post.md`](launch-post.md) (personal profile, not the Page).

---

## Smart Page settings

Do these once. Skip anything that looks like “we’re hiring” or ads until you mean it.

| Setting | Recommendation |
|---------|----------------|
| **Admin** | Nick = Super admin. Add a backup admin only if you trust the account. |
| **Notify employees** | On — so your personal profile is prompted to share Page posts. |
| **Page followers / invite** | Invite connections in batches (LinkedIn caps this). Prioritize platform, security, DevOps. |
| **Creator mode** | Company Pages don’t have it. Turn it **on** on the *personal* profile. |
| **Jobs** | Off until you are hiring. An empty Jobs tab looks unfinished. |
| **Services / products** | Optional later. If you add one: “Self-hosted agent runtime” → vaultrun.dev. No prices. |
| **Hashtags to follow as Page** | `#AIAgents` `#MCP` `#SelfHosted` `#PlatformEngineering` `#DevSecOps` |
| **Comment control** | Hide profanity / block words you don’t want. Don’t turn off comments. |
| **@mentions** | Allow — you want people tagging the Page. Review notification email daily the first week. |
| **Organic targeting** | Optional per post: job titles *Platform Engineer, Security Engineer, DevOps, SRE, CISO*; geos Netherlands + US + EU. Don’t over-narrow the first post. |
| **Analytics** | Check visitors / search keywords after ~20 followers. Use that to tweak specialties, not the tagline. |
| **Verify Page** | If LinkedIn offers it (domain vaultrun.dev) — do it. Trust badge next to the name. |
| **Showcase Pages** | None. One Page. |
| **Ads / Boost** | Don’t boost the first post. Get an organic baseline first. |
| **Employee tagline** | On Nick’s profile, list VaultRun as workplace so the logo shows on your posts. |

### Algorithm notes (so the first post isn’t dead on arrival)

- Native **image** > document carousel > text-only > **link-in-body**. External URLs in the post body suppress distribution. Links go in comment #1.
- Company Pages reach a fraction of a founder post. The Page is the brand home; Nick’s profile is the distribution.
- Reply to every early comment within a few hours. First-hour engagement is the ranking signal.
- Best window for a NL founder + international ICP: **Tue–Thu, 08:00–10:00 Europe/Amsterdam** (US East still waking, EU at desks).
- 3–5 hashtags, not 15. The set above is enough.
- Don’t edit the post after it starts getting impressions (edits can reset distribution). Fix typos in a comment if needed.

---

## What *not* to put on the Page

- Internal pricing, eval discounts, or “starting from €…”
- Customer logos or “used by …”
- Star counts you haven’t earned
- “Enterprise-grade AI platform” — prefer “on your infra”
- Conflating Apache 2.0 core with Enterprise SSO
