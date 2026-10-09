# Jev / OpenJEV integration

Status: **shipped** (opt-in). Claim-vs-evidence gates using a System One decision model — [TypeSafe Jev](https://docs.typesafe.ai/) or [OpenJEV](https://openjev.sh/) (`api.openjev.sh`). Same `/v1/systemone` wire format.

VaultRun remains the source of deterministic verify + sealed evidence; Jev judges whether an agent **claim** is backed by that evidence.

## Providers

| Provider | Default base | Default model | API key env |
|----------|--------------|---------------|-------------|
| **openjev** | `https://api.openjev.sh` | `openjev` | `OPENJEV_API_KEY` |
| **typesafe** | `https://api.typesafe.ai` | `jev-latest` | `TYPESAFE_API_KEY` |

Selection:

1. `JEV_PROVIDER=openjev|typesafe` if set
2. Else if only `OPENJEV_API_KEY` is set → **openjev**
3. Else → **typesafe**

Optional: `OPENJEV_BASE_URL` / `TYPESAFE_BASE_URL` / `JEV_BASE_URL`, `JEV_MODEL`.  
Self-hosted OpenJev (loopback): `JEV_ALLOW_PRIVATE_BASE=true`.

## Surfaces

1. **MCP** (`MCP_JEV_ENABLED=true`) — tools `jev_verify`, `jev_gate` (can load `verification_id` evidence)
2. **API** (`VAULTRUN_JEV_ENABLED=true`) — `POST /api/v1/verify/jev`, `POST /api/v1/verify/jev-gate`, `POST /api/v1/missions/:id/steps/verify`
3. **Local gateway** (`LOCAL_GATEWAY_JEV_ENABLED=true`) — completion gate before final answer (`LOCAL_GATEWAY_JEV_ON_FAIL=hold|fail`)

## Mission step config

```json
{
  "name": "install",
  "tool": "run_command",
  "verify": {
    "exit_code_zero": true,
    "stdout_contains": "Successfully",
    "jev_claim": "Dependencies installed successfully",
    "jev_min_noul": 0.75,
    "jev_on_fail": "hold"
  }
}
```

- Deterministic checks run first; on failure → HTTP **409** `fail`
- Jev under threshold → **409** (`fail`) or **202** (`hold`)

## Security

- Opt-in only; no ambient activation
- Public HTTPS bases by default (SSRF-safe dialer); OpenJEV/TypeSafe hosts allowlisted
- Outbound state truncated + secret redaction (`vr_`, `sk-`, `ts_`, `oj_`)
- Claim / evidence size caps; API key never logged
- Base URL never taken from request bodies

## Docs

- OpenJEV: https://openjev.sh/docs
- TypeSafe: https://docs.typesafe.ai/api
- Code: `internal/jev`, `sdk/mcp/jev.go`, `cmd/api/handlers/jev.go`, `internal/localgateway/jev_gate.go`
