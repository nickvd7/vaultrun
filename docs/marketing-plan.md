# VaultRun marketing plan

Status: **2026-08-14** · Product: [vaultrun.dev](https://vaultrun.dev) (open core **v0.3.x**; site JSON-LD still says 0.2.1 — bump on next site pass)

LinkedIn Page exists (vaultrun.dev) but is unfinished — kit: [`linkedin-page.md`](linkedin-page.md).

## Positioning

> The sandbox agents need, on infrastructure security already trusts.

VaultRun is the **self-hosted secure runtime** for AI agents — isolated Docker sandboxes, MCP-native (53+ tools; + browser / AWS / Flowd when enabled), HMAC audit trail, no SaaS telemetry. Local-first: successful agent work becomes **workflow assets** you keep (missions, verify checkpoints, sandbox memory, swarm graph). Enterprise SSO (OIDC + SAML) is the commercial upsell for teams with existing IdPs.

**Companion narrative:** [Flowd](https://flowd.net) handles local file workflow detection and approval; VaultRun handles execution blast radius. Together: observe → suggest → approve locally, run risky work in a container.

## Ideal customer profiles (ICP)

| ICP | Pain | VaultRun answer |
|-----|------|-----------------|
| **Platform engineering** | Agents need code execution without shared-VM risk | One API per env; MCP + SDKs; session isolation |
| **Security / compliance** | SaaS sandboxes leak data; ambient cloud creds are scary | Self-hosted; network off by default; signed audit |
| **Regulated sectors** | LLM tools in prod need controls | On-prem Docker; Enterprise SSO; policy hooks |

## What still holds vs what drifted

Reviewed against the **2026-07-16** draft:

| Still true | Needs a refresh |
|------------|-----------------|
| Positioning one-liner, ICPs, do/don’t, Enterprise funnel | Calendar assumed a v0.2 launch; product is **v0.3.x** (replay, browser MCP, NL policy, cost, templates, collab, missions) |
| “53+ MCP tools” (site + content kit) | README sometimes says 61 (= 53 core + 8 browser). Public copy stays **53+** until the site is bumped in one pass |
| No public pricing | LinkedIn was “post a launch” — not a **company Page** workstream. Page is now the bottleneck |
| Flowd companion, HN, demo video still pending | Raster brand files (`og.png`, lockup PNG, LinkedIn cover) were documented but **missing**; generator now in `scripts/generate-brand-pngs.py` |
| Site is live | Homepage JSON-LD `softwareVersion` is still `0.2.1` |

Do not invent social proof. Do not paste pricing.

## Channels (now)

| Phase | Actions | Status |
|-------|---------|--------|
| **Site** | Responsive site, `llms.txt`, README, PyPI | **Done** (version string on site still stale) |
| **LinkedIn Page** | Fill About + assets + first post ([linkedin-page.md](linkedin-page.md)) | **In progress** — Page created, copy/assets in this repo |
| **Founder distribution** | Nick personal post + Page reshare ([launch-post.md](launch-post.md)) | Next, same day as Page first post |
| **HN** | Show HN from [launch-post.md](launch-post.md) — weekday morning, not same hour as LinkedIn | After LinkedIn is live |
| **Demo** | Record [demo-video-script.md](demo-video-script.md); 15s LinkedIn cut | After launch posts |
| **MCP ecosystem** | awesome-mcp / directories; blog “self-hosted vs SaaS agent sandboxes” | After demo exists to embed |
| **Flowd** | Cross-promo vaultrun.dev/flowd.html ↔ flowd.net | Companion post week after launch |
| **Ongoing** | Enterprise funnel → `#contact` / mail@030.dev; [use-cases.html](../site/use-cases.html) | Always on |

## Content calendar (from LinkedIn go-live)

| Beat | Asset | Where |
|------|--------|--------|
| 0 | Page complete (logo, cover, About, button) | Company Page |
| 1 | First post + image + links in comment | Page, then Nick reshare |
| 2 | Founder long post (problem → opposite of SaaS sandbox) | Personal profile |
| 3 | Show HN | news.ycombinator.com |
| 4 | VaultRun + Flowd companion | Page + personal |
| 5 | CI runner / sandbox-in-CI deep-dive | Page + optional blog |
| 6 | Enterprise one-pager into security / platform communities | Not a “used by” post — repo + enterprise.html |

## Existing assets to reuse

- [brand guidelines](brand.md) + [`site/brand/`](../site/brand/) — logo, OG, social, LinkedIn, video end card
- [content kit](content-kit.md) — pitches, captions, signature, sales talking points
- [linkedin-page.md](linkedin-page.md) — Page fields, settings, first post, founder banner
- [launch-post.md](launch-post.md) — LinkedIn short/long, HN draft
- [demo-video-script.md](demo-video-script.md) — 2–3 min walkthrough
- [enterprise.html](../site/enterprise.html) — procurement one-pager
- [commercial.md](commercial.md) — packaging scaffold (pricing blocked)
- [use-cases.html](../site/use-cases.html) — reference patterns (no customer logos)
- [llms.txt](../site/llms.txt) — AI agent grounding

## Metrics

| Metric | Source |
|--------|--------|
| GitHub stars / forks | github.com/nickvd7/vaultrun |
| PyPI downloads | pypi.org/project/vaultrun-sdk |
| LinkedIn Page | Followers, unique visitors, post impressions (Page analytics) |
| LinkedIn founder | Impressions + profile views on the launch post |
| Enterprise inbound | mail@030.dev / contact form |
| MCP adoption | Self-report; HTTP `/` tool count endpoint |
| Site | Analytics if/when added to vaultrun.dev |
| HN | Points + comments on the Show HN |

## Messaging do / don't

**Do**

- Lead with self-hosted, audit trail, MCP
- Show `make up` quickstart — low friction
- Separate open core (Apache 2.0) from Enterprise SSO clearly
- On LinkedIn: native image, links in the first comment

**Don't**

- Paste internal pricing in public posts
- Claim customer logos without permission
- Position as "another ChatGPT wrapper"
- Put `https://` in the LinkedIn post body on launch day (distribution penalty)

## Enterprise funnel

1. **Evaluate** — free dev/test SSO (mail@030.dev)
2. **License** — production quote (org, IdP, scale, timeline)
3. **Schedule** — contact form with preferred slots

All paths: [vaultrun.dev/#contact](https://vaultrun.dev/#contact)

## Competitive framing (internal)

| Alternative | Gap VaultRun fills |
|-------------|------------------|
| SaaS code runners | Data leaves network; vendor telemetry |
| Raw Docker on dev laptops | No audit, no agent API, no MCP |
| CI-only sandboxes | No interactive agent sessions |

## Next actions (checklist)

- [x] Publish responsive site
- [ ] Finish LinkedIn Page (logo, cover, About, Visit website) — [linkedin-page.md](linkedin-page.md)
- [ ] Post launch on LinkedIn Page + founder reshare
- [ ] Show HN
- [ ] Record demo video
- [ ] Bump site JSON-LD / twitter copy from v0.2.1 / “53-tool” to current
- [ ] Submit to MCP tool directories
- [ ] Cross-link Flowd companion page
- [ ] Track first 5 enterprise inbound conversations
