# Contributing to VaultRun

Thanks for your interest! Issues and pull requests are welcome.

## Getting started

```bash
git clone https://github.com/nickvd7/vaultrun
cd vaultrun
cp .env.example .env     # set MASTER_API_KEY
make up                  # API + Postgres + Redis + dashboard
```

Prerequisites: Docker, Docker Compose, Go 1.25+.

## Before you open a PR

```bash
make fmt vet            # format + vet
make lint               # golangci-lint
make test               # unit tests
go test ./sdk/mcp/...   # MCP server tests
cd sdk/python && pip install -e ".[dev]" && pytest   # Python SDK tests
```

- Keep PRs focused — one change per PR.
- Add or update tests for anything you change.
- Update `CHANGELOG.md` under `[Unreleased]` and relevant docs in `docs/`.
- CI must be green before review.

## Reporting bugs & requesting features

Use the GitHub issue forms (Bug report / Feature request):

→ https://github.com/nickvd7/vaultrun/issues/new/choose

| Topic | Where |
|-------|--------|
| Bugs & features (open core) | [GitHub Issues](https://github.com/nickvd7/vaultrun/issues) |
| Security vulnerabilities | [SECURITY.md](SECURITY.md) — private advisory or `mail@030.dev` with `[SECURITY] vaultrun` |
| Enterprise / licensing / sales | [vaultrun.dev/#contact](https://vaultrun.dev/#contact) or `mail@030.dev` |

Please include VaultRun version, component, and reproduction steps for bugs. Never paste API keys or secrets.

## License

By contributing you agree that your contributions are licensed under the
[Apache 2.0 license](LICENSE). (Enterprise features live in a separate,
privately licensed repository and are not part of this codebase.)
