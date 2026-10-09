# Local Inference Gateway — client recipes

Point any OpenAI-compatible client at the VaultRun local gateway.

```bash
make build-local
export LOCAL_GATEWAY_AUTH_TOKEN="$(openssl rand -hex 16)"
export VAULTRUN_BASE_URL=http://127.0.0.1:8080
export VAULTRUN_API_KEY=vr_...
export LOCAL_GATEWAY_UPSTREAM_URL=http://127.0.0.1:11434
export LOCAL_GATEWAY_DEFAULT_MODEL=llama3.2
./bin/vaultrun-local
```

Base URL for clients: `http://127.0.0.1:8091/v1`  
Auth: `Authorization: Bearer $LOCAL_GATEWAY_AUTH_TOKEN`

| File | Client |
|------|--------|
| [open-webui.env](open-webui.env) | Open WebUI connection env |
| [continue-config.yaml](continue-config.yaml) | Continue.dev `config.yaml` snippet |
| [litellm-config.yaml](litellm-config.yaml) | LiteLLM proxy model entry |

Automated smoke (no Ollama required):

```bash
make test-local-gateway
# or
./scripts/local-gateway-smoke.sh
```
