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
├── .codex-plugin/
│   └── plugin.json          # OpenAI Codex / ChatGPT plugin manifest
├── .agents/
│   ├── skills/vaultrun → ../../skills/vaultrun   # Codex repo skill discovery
│   └── plugins/marketplace.json                 # Codex repo marketplace
├── skills/vaultrun/
│   ├── SKILL.md             # Canonical skill (all marketplaces)
│   ├── reference.md
│   └── agents/openai.yaml   # Codex UI metadata
├── .cursor/skills/vaultrun/ # Project copy (Cursor repo contributors)
└── AGENTS.md                # Cross-tool agent context (Cursor, Claude, Codex)
```

**Canonical skill for marketplaces:** `skills/vaultrun/`. When you edit the skill, update both `skills/vaultrun/` and `.cursor/skills/vaultrun/`, then bump `version` in `.cursor-plugin`, `.claude-plugin`, and `.codex-plugin` manifests.

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

## OpenAI Codex / ChatGPT

Codex discovers repo skills under `.agents/skills/` (this repo symlinks to `skills/vaultrun`).
`AGENTS.md` at the repo root is also loaded as project guidance.

### Test locally

1. Open the VaultRun repo in Codex CLI or ChatGPT desktop (Work / Codex mode)
2. Confirm skill `vaultrun` appears in Skills / `$` mention
3. Try: `Help me configure VaultRun MCP for Claude Desktop`

Repo marketplace (ChatGPT desktop Plugins Directory):

- Catalog: `.agents/plugins/marketplace.json`
- Plugin root: repo itself (`.codex-plugin/plugin.json` + `skills/`)

```bash
# if you have the Codex CLI
codex plugin marketplace add .
# or: nickvd7/vaultrun
```

### Workspace sharing

There is no public OpenAI skills marketplace yet. Distribute via:

- Clone of this repo (`.agents/skills` + `AGENTS.md`)
- Team / Enterprise ChatGPT workspace plugin sharing
- Your own curated marketplace pointing at this GitHub repo

Docs: [OpenAI Codex skills](https://developers.openai.com/codex/skills) · [Build plugins](https://developers.openai.com/codex/plugins/build)

---

## Versioning

Bump `version` in:

- `.cursor-plugin/plugin.json`
- `.claude-plugin/plugin.json`
- `.codex-plugin/plugin.json`
- `.cursor-plugin/marketplace.json` (plugin entry)
- `.claude-plugin/marketplace.json` (plugin entry)

Or omit `version` to pin to git commit SHA (auto-updates on install).

---

## Validation checklist (run before submit)

```bash
# JSON manifests parse
python3 -c 'import json,pathlib; [json.loads(p.read_text()) for p in pathlib.Path(".").rglob("plugin.json")]'

# Skill frontmatter + size
test -f skills/vaultrun/SKILL.md
test -f .agents/skills/vaultrun/SKILL.md   # symlink
wc -l skills/vaultrun/SKILL.md             # should be < 500
```

Manual:

- [ ] Cursor: `ln -s $(pwd) ~/.cursor/plugins/local/vaultrun` → Customize shows skill
- [ ] Claude Code: `/plugin marketplace add .` → install `vaultrun@vaultrun-plugins`
- [ ] Codex: open repo → skill `vaultrun` in Skills / `$vaultrun`
- [ ] `SKILL.md` description includes trigger terms (vaultrun, MCP, sandbox, flowd)
- [ ] No secrets in skill or reference files
- [ ] Marketplace skill links use https:// (not `../../` in `skills/vaultrun/`)

---

## Other tools

| Tool | How users get context |
|------|----------------------|
| **Repo clone** | `.cursor/skills/`, `.agents/skills/`, `AGENTS.md` |
| **Cursor Marketplace** | Submit via cursor.com/marketplace/publish |
| **Claude Code** | `/plugin marketplace add nickvd7/vaultrun` |
| **OpenAI Codex** | `.agents/skills` + `.codex-plugin` + `AGENTS.md` |
| **Any LLM crawler** | https://vaultrun.dev/llms.txt |

Contact: mail@030.dev
