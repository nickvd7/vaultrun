# Local Inference Action Gateway

**Status:** shipped (sidecar)  
**Package:** `internal/localgateway` · **Binary:** `cmd/local` (`vaultrun-local`)  
**Ops guide:** [../local-gateway.md](../local-gateway.md)

## Summary

Additive OpenAI-compatible HTTP gateway that sits between local inference (Ollama, LM Studio, vLLM, …) and the VaultRun API. The model thinks; VaultRun sandboxes do.

## Scope (v1)

- `POST /v1/chat/completions` with injected VaultRun tools (JSON or final-answer SSE)
- `GET /v1/models` (proxy or default-model fallback)
- `GET /healthz`
- Auto session create / sticky conversation / explicit session pin
- Optional mission auto-capture from successful tool loops
- Bearer auth, per-IP rate limit, body/tool/loop caps, path & command validation
- Client recipes: `examples/local-gateway/`

## Non-goals

- Model hosting, SQLite lite mode, dashboard chat UI
