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

`pipeline_discriminates` is true only when every control is consistent **and** at least one positive was accepted and one negative rejected.

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` / `POST` | `/api/v1/verify/controls` | Run suite (auth required; no session) |

Response includes `suite_id` (`vaultrun-verify-controls-v1`), `fingerprint` (SHA-256 of frozen defs), and per-control outcomes.

HTTP **500** if the suite fails (invariant break). **200** when it passes.

## MCP

Tool: `verify_controls` — no arguments; calls the API above.

## Code

- `internal/verify/controls.go` — suite + `RunControls()`
- `cmd/api/handlers/verify.go` — `Controls`
- `sdk/mcp/verify.go` — MCP tool

Changing control cases **must** bump `SuiteID` and update the pinned fingerprint in `controls_test.go`.
