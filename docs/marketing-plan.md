# VaultRun marketing plan

Status: **2026-07-16** · Product: [vaultrun.dev](https://vaultrun.dev)

## Positioning

> The sandbox agents need, on infrastructure security already trusts.

VaultRun is the **self-hosted secure runtime** for AI agents — isolated Docker sandboxes, MCP-native (53+ tools), HMAC audit trail, no SaaS telemetry. Enterprise SSO (OIDC + SAML) is the commercial upsell for teams with existing IdPs.

**Companion narrative:** [Flowd](https://flowd.net) handles local file workflow detection and approval; VaultRun handles execution blast radius. Together: observe → suggest → approve locally, run risky work in a container.

## Ideal customer profiles (ICP)

| ICP | Pain | VaultRun answer |
|-----|------|-----------------|
| **Platform engineering** | Agents need code execution without shared-VM risk | One API per env; MCP + SDKs; session isolation |
| **Security / compliance** | SaaS sandboxes leak data; ambient cloud creds are scary | Self-hosted; network off by default; signed audit |
| **Regulated sectors** | LLM tools in prod need controls | On-prem Docker; Enterprise SSO; policy hooks |

## Channels (90 days)

| Phase | Actions |
|-------|---------|
| **Week 1–2** | Responsive site live; update `llms.txt`; GitHub README; PyPI SDK |
| **Week 2–3** | LinkedIn + HN launch ([launch-post.md](launch-post.md)); record [demo-video-script.md](demo-video-script.md) |
| **Week 3–6** | MCP ecosystem: awesome-mcp PRs; blog "self-hosted vs SaaS agent sandboxes" |
| **Week 4–8** | Flowd cross-promo: vaultrun.dev/flowd.html ↔ flowd.net |
| **Ongoing** | Enterprise funnel → `#contact` / mail@030.dev; [use-cases.html](../site/use-cases.html) as sales collateral |

## Content calendar (month 1)

| Week | Asset |
|------|-------|
| 1 | Launch post + responsive site + skill/AGENTS.md for AI coders |
| 2 | "VaultRun + Flowd" technical companion post |
| 3 | CI runner use-case deep-dive |
| 4 | Enterprise one-pager in security / platform eng communities |

## Existing assets to reuse

- [launch-post.md](launch-post.md) — LinkedIn short/long, HN draft
- [demo-video-script.md](demo-video-script.md) — 2–3 min walkthrough
- [enterprise.html](../site/enterprise.html) — procurement one-pager
- [use-cases.html](../site/use-cases.html) — reference patterns (no customer logos)
- [llms.txt](../site/llms.txt) — AI agent grounding

## Metrics

| Metric | Source |
|--------|--------|
| GitHub stars / forks | github.com/nickvd7/vaultrun |
| PyPI downloads | pypi.org/project/vaultrun-sdk |
| Enterprise inbound | mail@030.dev / contact form |
| MCP adoption | Self-report; HTTP `/` tool count endpoint |
| Site | Analytics if/when added to vaultrun.dev |

## Messaging do / don't

**Do**

- Lead with self-hosted, audit trail, MCP
- Show `make up` quickstart — low friction
- Separate open core (Apache 2.0) from Enterprise SSO clearly

**Don't**

- Paste internal pricing in public posts
- Claim customer logos without permission
- Position as "another ChatGPT wrapper"

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

- [ ] Publish responsive site
- [ ] Post launch on LinkedIn + HN
- [ ] Record demo video
- [ ] Submit to MCP tool directories
- [ ] Cross-link Flowd companion page
- [ ] Track first 5 enterprise inbound conversations
