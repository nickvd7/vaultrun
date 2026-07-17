# VaultRun — agent guide

Self-hosted secure runtime for AI agents. Isolated Docker sandboxes, MCP server (stdio + HTTP), HMAC audit trail. Apache 2.0 open core.

## For AI coding assistants

- **Skill:** [.cursor/skills/vaultrun/SKILL.md](.cursor/skills/vaultrun/SKILL.md) — detailed repo map and conventions
- **Marketplace plugin:** [skills/vaultrun/](skills/vaultrun/) — Cursor / Claude / Codex ([docs/plugin-publish.md](docs/plugin-publish.md))
- **OpenAI Codex:** [.agents/skills/vaultrun](.agents/skills/vaultrun) (symlink) + [AGENTS.md](AGENTS.md)
- **LLM grounding:** [site/llms.txt](site/llms.txt) and [site/llms-full.txt](site/llms-full.txt)

## Build & test

```bash
cp .env.example .env   # set MASTER_API_KEY
make up                # API + Postgres + Redis + dashboard
make bootstrap-key     # first vr_... API key
make test              # unit + integration tests
```

Dashboard: http://localhost:3000 · API: http://localhost:8080

## Conventions

- Go 1.25, Gin API, Next.js dashboard (Tailwind)
- Minimize diff scope; match existing patterns
- Never commit secrets (`.env`, API keys)
- Enterprise SSO lives in a separate commercial overlay — see [docs/sso-setup.md](docs/sso-setup.md)

## Key paths

| Path | Purpose |
|------|---------|
| `cmd/api/` | REST API |
| `sdk/mcp/` | MCP server (53+ tools; +6 Flowd when enabled) |
| `apps/frontend/` | Dashboard |
| `site/` | Marketing static site |
| `docs/` | Architecture, security, OpenAPI |

## Flowd integration

Optional MCP bridge to local [Flowd](https://flowd.net) via `flowctl`. See [docs/flowd-integration.md](docs/flowd-integration.md).

## Links

- Site: https://vaultrun.dev
- Issues: https://github.com/nickvd7/vaultrun/issues
- Contact: mail@030.dev
