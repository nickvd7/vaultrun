# Killer Features — shipped (v0.3.0 / v0.3.1)

**All six features below are implemented** in VaultRun open core. Specs in this folder remain the design reference; treat status as **shipped**, not open proposals.

Roadmap: [../roadmap.md](../roadmap.md) · Security verification: [../security-testing-report.md](../security-testing-report.md)

## Status

| Feature | Status | Primary code |
|---------|--------|----------------|
| Session Replay & Time-Travel | ✅ Shipped | `internal/replay`, handlers/replay |
| Browser Automation | ✅ Shipped | `internal/browser`, `sdk/mcp/browser.go` |
| Multi-Agent Collaboration | ✅ Shipped | `internal/collab` (WebSocket + Redis) |
| Natural Language Policy | ✅ Shipped | `internal/nlpolicy` |
| Cost Intelligence | ✅ Shipped | `internal/cost`, dashboard costs page |
| Session Templates | ✅ Shipped | `internal/templates` |
| Verify checkpoints | ✅ Shipped | `internal/verify`, MCP `verify_checkpoint` |
| Agent memory (sandbox) | ✅ Shipped | MCP `memory_*` → `.vaultrun/memory/` |

**Progress: 6/6 shipped** (v0.3.0), hardened in v0.3.1. Verify checkpoints and agent memory added later as workflow foundation.

---

## Feature specs

1. **[session-replay.md](session-replay.md)** — checkpoints, restore/fork
2. **[browser-automation.md](browser-automation.md)** — headless browser + MCP tools · also [../browser-automation.md](../browser-automation.md)
3. **[multi-agent-collaboration.md](multi-agent-collaboration.md)** — presence + messaging
   - also **[agent-swarm-graph.md](agent-swarm-graph.md)** — topology edges
4. **[natural-language-policy.md](natural-language-policy.md)** — LLM → OPA/iptables · also [../natural-language-policy.md](../natural-language-policy.md)
5. **[cost-intelligence.md](cost-intelligence.md)** — metrics, budgets, alerts
6. **[session-templates.md](session-templates.md)** — template marketplace API
7. **[verify-checkpoints.md](verify-checkpoints.md)** — post-run assertions for missions / MCP
8. **[agent-memory.md](agent-memory.md)** — sandbox `.vaultrun/memory/` MCP tools
9. **[workflow-as-asset.md](workflow-as-asset.md)** — local-first positioning for missions / verify / memory / swarm / cost

## Supporting documentation

- [SECURITY_REVIEW.md](SECURITY_REVIEW.md) — threat model notes from the design phase
- [IMPLEMENTATION_GUIDE.md](IMPLEMENTATION_GUIDE.md) — historical build guide
- [TEST_SCENARIOS.md](TEST_SCENARIOS.md) — scenario catalogue
- [DEPLOYMENT_CHECKLIST.md](DEPLOYMENT_CHECKLIST.md) — production checklist

These guides still use “proposal” language in places; prefer the table above and [CHANGELOG.md](../../CHANGELOG.md) for what actually shipped.
