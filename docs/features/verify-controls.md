# Verify controls (evaluator self-check)

Status: **shipped**. Frozen positive/negative controls that certify the VaultRun verify evaluator itself.

Inspired by verification-first patterns (e.g. FaultWright Demo V0): prove the *pipeline discriminates* known-good from known-bad under one frozen suite identity — before trusting `verify_checkpoint` on agent runs.

## Idea

| Control | Kind | Expectation |
|---------|------|-------------|
| `reference-repair` | positive | PASS (exit 0 + stdout + file) |
| `no-repair` | negative | FAIL (broken exit/stdout/file) |
| `stdout-miss` | negative | FAIL (exit 0 but missing marker) |
| `nonzero-required` | positive | PASS (spec wants non-zero) |
| `file-missing-despite-green` | negative | FAIL (green exit/stdout, missing file) — v2 anti-shortcut |
| `stderr-only-success` | negative | FAIL (marker only on stderr) — v2 |
| `exit-nonzero-with-file` | negative | FAIL (artifact present, exit 1) — v2 |
| `wrong-filename` | negative | FAIL (similar path, not exact) — v2 |

`pipeline_discriminates` is true only when every control is consistent **and** at least one positive was accepted and one negative rejected.

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` / `POST` | `/api/v1/verify/controls` | Run suite (auth required; no session) |

Response includes `suite_id` (`vaultrun-verify-controls-v2`), `fingerprint` (SHA-256 of frozen defs), and per-control outcomes.

HTTP **500** if the suite fails (invariant break). **200** when it passes.

## MCP

Tool: `verify_controls` — no arguments; calls the API above.

Related: sealed evidence export with optional control embedding — [verify-evidence.md](verify-evidence.md).

## Code

- `internal/verify/controls.go` — suite + `RunControls()`
- `cmd/api/handlers/verify.go` — `Controls`
- `sdk/mcp/verify.go` — MCP tool

Changing control cases **must** bump `SuiteID` and update the pinned fingerprint in `controls_test.go`.
