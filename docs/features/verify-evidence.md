# Verify evidence export

Status: **shipped**. Digests-and-optional-HMAC sealed verification records (FaultWright-style evidence packaging).

## Idea

Export a portable, integrity-checked record of a verify checkpoint and/or the frozen control suite:

- `content_digest` — SHA-256 of canonical JSON with digest/sig cleared
- `sig` — HMAC-SHA256 over the digest hex when `AUDIT_HMAC_KEY` is set
- Truncates oversized stdout/stderr; strips NUL bytes

Schema: `vaultrun.verify.evidence.v1`

## API

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/verify/evidence` | Build sealed export from inline spec, `verification_id`, and/or `include_controls` |
| `GET` | `/api/v1/verifications/:id/evidence` | Export a persisted verification (`?include_controls=true`, `?sign=false`) |

### POST body

```json
{
  "verification_id": "…",
  "spec": { "exit_code_zero": true, "stdout_contains": "Successfully" },
  "observation": { "exit_code": 0, "stdout": "…" },
  "run_id": "…",
  "session_id": "…",
  "step_name": "install",
  "include_controls": true,
  "sign": true
}
```

Provide at least one of: `verification_id`, non-empty `spec`, or `include_controls: true`.

### ACL

- Reading a persisted verification requires **viewer** access to its session (same 404-on-deny pattern as sessions).
- Session-less records: **master** only.
- Controls-only exports need auth but no session.

## MCP

Tool: `verify_evidence` — same sources via tools/call.

## Integrity check (offline)

```go
err := verify.VerifyEvidenceIntegrity(rec, []byte(os.Getenv("AUDIT_HMAC_KEY")))
```

## Code

- `internal/verify/evidence.go` — `BuildEvidence`, digests, HMAC
- `cmd/api/handlers/verify.go` — `Evidence`, `GetEvidence`
- `sdk/mcp/verify.go` — MCP tool
