# Local Inference Action Gateway

OpenAI-compatible **action plane** for local AI (Ollama, LM Studio, vLLM, llama.cpp server, …). The gateway proxies chat to your local model and executes VaultRun sandbox tools whenever the model returns `tool_calls`.

> **Ollama denkt. VaultRun doet.**

This is an additive sidecar (`cmd/local`). It does **not** host models, replace Postgres, or change the core API.

## Architecture

```
┌──────────────────┐     ┌─────────────────────────┐     ┌─────────────────┐
│ Open WebUI /     │────▶│ vaultrun-local gateway  │────▶│ Ollama / LM     │
│ Continue / curl  │     │ /v1/chat/completions    │     │ Studio / vLLM   │
└──────────────────┘     └───────────┬─────────────┘     └─────────────────┘
                                     │ tool_calls
                                     ▼
                            ┌─────────────────┐
                            │ VaultRun API    │
                            │ Docker sandbox  │
                            └─────────────────┘
```

## Prerequisites

1. VaultRun API running (`make up && make bootstrap-key`)
2. A local OpenAI-compatible inference server (e.g. `ollama serve`)
3. Go 1.25+ to build the gateway

## Quickstart

```bash
go build -o bin/vaultrun-local ./cmd/local

export LOCAL_GATEWAY_AUTH_TOKEN="$(openssl rand -hex 16)"
export VAULTRUN_BASE_URL=http://localhost:8080
export VAULTRUN_API_KEY=vr_your_key
export LOCAL_GATEWAY_UPSTREAM_URL=http://127.0.0.1:11434
export LOCAL_GATEWAY_DEFAULT_MODEL=llama3.2

./bin/vaultrun-local
```

Point any OpenAI-compatible client at `http://localhost:8091/v1` with bearer token `LOCAL_GATEWAY_AUTH_TOKEN`.

```bash
curl -s http://localhost:8091/v1/chat/completions \
  -H "Authorization: Bearer $LOCAL_GATEWAY_AUTH_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-VaultRun-Conversation-Id: demo-1" \
  -d '{
    "model": "llama3.2",
    "stream": false,
    "messages": [{"role":"user","content":"Create hi.txt with hello and then cat it"}]
  }'
```

The response includes `X-VaultRun-Session-Id` so you can reuse the same sandbox.

## Injected tools

Every request gets these VaultRun tools (client tools with the same names are ignored):

| Tool | Purpose |
|------|---------|
| `vaultrun_run_command` | Docker exec (command + argv, no shell) |
| `vaultrun_write_file` | Write UTF-8 file in workspace |
| `vaultrun_read_file` | Read UTF-8 file |
| `vaultrun_list_files` | List workspace files |
| `vaultrun_session_info` | Current session id/status |

Auto-created sessions use `LOCAL_GATEWAY_DEFAULT_IMAGE` (default `python:3.12-slim`) with **network disabled** unless `LOCAL_GATEWAY_NETWORK_ENABLED=true`.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LOCAL_GATEWAY_AUTH_TOKEN` | *(required)* | Bearer token (≥16 chars) |
| `VAULTRUN_BASE_URL` | *(required)* | VaultRun API base URL |
| `VAULTRUN_API_KEY` | *(required)* | VaultRun API key (`vr_…`) |
| `LOCAL_GATEWAY_UPSTREAM_URL` | `http://127.0.0.1:11434` | OpenAI-compat upstream (operator-only; never from request body) |
| `LOCAL_GATEWAY_PORT` | `127.0.0.1:8091` | Listen address (loopback by default; set `:8091` only if LAN bind is intentional) |
| `LOCAL_GATEWAY_DEFAULT_MODEL` | — | Used when client omits `model` |
| `LOCAL_GATEWAY_DEFAULT_IMAGE` | `python:3.12-slim` | Sandbox image for auto sessions |
| `LOCAL_GATEWAY_NETWORK_ENABLED` | `false` | Enable sandbox network |
| `LOCAL_GATEWAY_MAX_TOOL_LOOPS` | `8` | Cap model↔tool iterations (1–32) |
| `LOCAL_GATEWAY_RATE_LIMIT` | `60` | Per-IP requests / minute |
| `LOCAL_GATEWAY_MAX_BODY_BYTES` | `1048576` | Max request body |
| `LOCAL_GATEWAY_UPSTREAM_TIMEOUT_SEC` | `120` | Upstream chat timeout |
| `LOCAL_GATEWAY_RUN_TIMEOUT_SEC` | `60` | Default sandbox run timeout |
| `LOCAL_GATEWAY_MAX_RUN_TIMEOUT_SEC` | `300` | Hard cap for run timeouts |
| `LOCAL_GATEWAY_CAPTURE_MISSIONS` | `true` | Persist successful VaultRun tool sequences as missions |
| `LOCAL_GATEWAY_JEV_ENABLED` | `false` | Opt-in completion gate via OpenJEV / TypeSafe (`OPENJEV_API_KEY` or `TYPESAFE_API_KEY`) |
| `LOCAL_GATEWAY_JEV_MIN_NOUL` | `0.7` | Minimum noul for claim + evidence_backed |
| `LOCAL_GATEWAY_JEV_ON_FAIL` | `hold` | `hold` (retry then 409) or `fail` (409) |
| `LOCAL_GATEWAY_JEV_MAX_RETRIES` | `1` | Extra tool-loop rounds after a failed gate |
| `JEV_PROVIDER` | *(auto)* | `openjev` or `typesafe` — see [jev-integration.md](features/jev-integration.md) |

## Streaming

`stream: true` is supported for the **final assistant answer** (OpenAI SSE). Tool rounds always call the upstream non-streaming, then the gateway emits `text/event-stream` chunks ending with `data: [DONE]`.

## Mission capture

When at least one VaultRun tool succeeds in a loop, the gateway creates a published mission (`local-…` slug) and records a mission run linked to the session. Disable with `LOCAL_GATEWAY_CAPTURE_MISSIONS=false`.

## Client recipes

See [`examples/local-gateway/`](../examples/local-gateway/) for Open WebUI, Continue.dev, and LiteLLM.

## Headers

| Header | Direction | Meaning |
|--------|-----------|---------|
| `Authorization: Bearer …` | request | Gateway auth (required) |
| `X-VaultRun-Conversation-Id` | request | Sticky session key (optional) |
| `X-VaultRun-Session-Id` | request | Pin to an existing session UUID (optional) |
| `X-VaultRun-Session-Id` | response | Session used for this completion |

## Security posture

- Default bind is **loopback only** (`127.0.0.1:8091`); startup warns on non-loopback binds
- Bearer auth required for all `/v1/*` (healthz is open)
- Rate limit runs **before** auth (brute-force resistant)
- `X-Forwarded-For` is **not** trusted by default
- Upstream URL is **config-only** (clients cannot redirect inference)
- Upstream redirects are never followed
- Streaming: tool rounds buffered; only the final answer is SSE-streamed
- Workspace paths sanitized against traversal; tool JSON rejects unknown fields
- Commands passed as argv to VaultRun (no shell)
- Tool loop + body/file/result size caps
- Security response headers (`nosniff`, `DENY` frame, CSP `default-src 'none'`, `no-store`)

## Non-goals (v1)

- Hosting or downloading models
- SQLite / zero-Postgres laptop mode
- Dashboard chat UI

## Related

- [Flowd integration](flowd-integration.md) — local file-workflow bridge via MCP
- [MCP server](mcp.md) — full tool surface for MCP-native agents
- [Security model](security.md)
