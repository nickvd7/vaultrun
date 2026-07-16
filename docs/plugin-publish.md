# Publishing the VaultRun agent plugin

This repo ships a distributable **agent plugin** with the `vaultrun` skill for Cursor, Claude Code, and other assistants that support the Open Agent Skills / plugin format.

## What's in the plugin

```
vaultrun/
├── .cursor-plugin/
│   ├── plugin.json          # Cursor plugin manifest
│   └── marketplace.json     # Cursor Team Marketplace catalog
├── .claude-plugin/
│   ├── plugin.json          # Claude Code plugin manifest
│   └── marketplace.json     # Claude marketplace catalog
├── skills/vaultrun/
│   ├── SKILL.md             # Canonical skill (marketplace installs)
│   └── reference.md         # Env vars, API summary
├── .cursor/skills/vaultrun/ # Project copy (repo contributors)
└── AGENTS.md                # Cross-tool agent context
```

**Canonical skill for marketplaces:** `skills/vaultrun/`. When you edit the skill, update both `skills/vaultrun/` and `.cursor/skills/vaultrun/`, then bump `version` in both `plugin.json` files.

---

## Cursor

### Test locally

```bash
mkdir -p ~/.cursor/plugins/local
ln -sf "$(pwd)" ~/.cursor/plugins/local/vaultrun
# Restart Cursor or Developer: Reload Window
```

Open **Customize** → verify skill `vaultrun` appears. Invoke with `/vaultrun` in chat.

### Public Marketplace

1. Ensure repo is public on GitHub
2. Submit at **https://cursor.com/marketplace/publish**
3. Wait for manual review (open source required)

### Team Marketplace (Teams / Enterprise)

1. Cursor Dashboard → **Plugins** → **Add Marketplace**
2. **Import from Repo** → `https://github.com/nickvd7/vaultrun`
3. Cursor reads `.cursor-plugin/marketplace.json`
4. Assign plugins to access groups; enable **Auto Refresh** for push updates

### Community listing

- **https://cursor.directory** — add repo URL for discovery (no official review)

---

## Claude Code

### Test locally

```bash
claude --plugin-dir .
# or from another project:
claude plugin validate .
```

Install from local marketplace:

```bash
/plugin marketplace add ./path/to/vaultrun
/plugin install vaultrun@vaultrun-plugins
```

Skills are namespaced: `/vaultrun:…` when invoked as commands.

### Your own marketplace

Users add the catalog:

```bash
/plugin marketplace add nickvd7/vaultrun
/plugin install vaultrun@vaultrun-plugins
```

Validate before publishing:

```bash
claude plugin validate .
```

### Community marketplace

Submit to Anthropic's community catalog (`anthropics/claude-plugins-community`) after review. See [Claude Code plugin docs](https://code.claude.com/docs/en/plugins).

---

## Versioning

Bump `version` in:

- `.cursor-plugin/plugin.json`
- `.claude-plugin/plugin.json`
- `.cursor-plugin/marketplace.json` (plugin entry)
- `.claude-plugin/marketplace.json` (plugin entry)

Or omit `version` to pin to git commit SHA (auto-updates on install).

---

## Checklist before submit

- [ ] `skills/vaultrun/SKILL.md` description includes trigger terms (vaultrun, MCP, sandbox, flowd)
- [ ] SKILL.md under 500 lines
- [ ] No secrets in skill or reference files
- [ ] Links use `https://vaultrun.dev` or GitHub URLs (not relative `../../` paths)
- [ ] `plugin.json` name is `vaultrun` (lowercase, no spaces)
- [ ] Repo is public (Cursor Marketplace requirement)
- [ ] Tested locally in Cursor and/or Claude Code

---

## Other tools

| Tool | How users get context |
|------|----------------------|
| **Repo clone** | `.cursor/skills/vaultrun/` + `AGENTS.md` auto-loaded |
| **Codex / OpenAI** | Point at `AGENTS.md` or `site/llms.txt` |
| **Any MCP client** | `https://vaultrun.dev/llms.txt` |

Contact: mail@030.dev
