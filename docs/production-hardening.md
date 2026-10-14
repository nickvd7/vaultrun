# Production hardening checklist

The defaults are tuned for a local quickstart: `make up` and it works. Several of them are too open for a deployment that untrusted clients can reach. This page lists what to change and what VaultRun can enforce for you.

## Secure by default

The API refuses to start unless all of these hold:

| Check | Satisfy it with | Explicit opt-out |
|-------|-----------------|------------------|
| OPA policy | `OPA_POLICY_FILE=/path/policy.rego` | `OPA_ALLOW_ALL=true` (accept AllowAll on purpose) |
| TLS | `TLS_CERT_FILE` + `TLS_KEY_FILE`, or `ACME_DOMAIN` | `TLS_DISABLED=true` (a reverse proxy terminates TLS) |
| `/metrics` auth | `METRICS_TOKEN=<random>` (bearer token) | none |
| Master key | if set, at least 32 characters (`openssl rand -hex 32`); empty is allowed once you have bootstrapped | none |
| Redis password | `REDIS_PASSWORD`, checked only when `REDIS_ADDR` is set | none |

Every failed check is logged as `insecure configuration` with the variable to set.

### Local development

`.env.example` contains `VAULTRUN_INSECURE_DEV=true`, so `cp .env.example .env && make up` works with open defaults. With it set, the API starts anyway and logs one warning listing what is open. **Remove that line for any deployment other people can reach.**

If you start the API without that variable (your own `.env`, the release binary, your own compose file) and the checks fail, it exits with the list of problems instead of running open.

Upgrading from 0.3.4 or earlier: an existing `.env` without `VAULTRUN_INSECURE_DEV` now has to either pass the checks or add that line.

## Not covered by the startup check

These need action on your side:

- **Workspace permissions (0777 session directories).** This is intentional and cannot be tightened to 0700/0750 today. Sandboxes run as `nobody` (UID 65534), the API as another UID; the bind-mounted session directory is only writable from the sandbox if the "other" bits are set. The exposure is limited by the parent directory (`WORKSPACE_BASE_DIR`), which is created `0700` (owner only), so other host users cannot list or reach session directories. Keep that parent directory private, keep the host single-tenant, and do not mount it anywhere else. A per-session UID with user namespaces would remove the need for 0777; it is not implemented. Details: `internal/workspace/workspace.go`.
- **Docker socket.** The API needs access to `/var/run/docker.sock`, which is effectively root on the host. Run VaultRun on a dedicated host or VM.
- **Image allowlist.** Set `DOCKER_IMAGE_ALLOWLIST` to the images you approve, and `COSIGN_PUBLIC_KEY` to fail closed on unsigned ones.
- **Database TLS.** Set `DB_SSL_MODE=verify-full` with `DB_SSL_ROOT_CERT`.
- **Network exposure.** Bind to a private interface or put the API behind a firewall; only expose 443 to trusted callers. The compose file already binds to `127.0.0.1`.
- **Audit integrity.** Set `AUDIT_HMAC_KEY` so audit entries are signed.
- **Webhooks.** Set `WEBHOOK_SECRET` for async callbacks.

The longer list, including SSO and the MCP HTTP transport, is in [security.md](security.md#recommendations-for-production).

## Minimal production `.env`

```bash
MASTER_API_KEY=<openssl rand -hex 32>
METRICS_TOKEN=<openssl rand -hex 32>
OPA_POLICY_FILE=/etc/vaultrun/policy.rego
TLS_CERT_FILE=/etc/vaultrun/tls/fullchain.pem
TLS_KEY_FILE=/etc/vaultrun/tls/privkey.pem
DB_SSL_MODE=verify-full
DB_SSL_ROOT_CERT=/etc/vaultrun/db-ca.pem
DOCKER_IMAGE_ALLOWLIST=python:3.12-slim,node:20-slim
AUDIT_HMAC_KEY=<openssl rand -hex 32>
```
