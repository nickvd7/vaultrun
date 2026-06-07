# VaultRun Configuration Reference

All configuration is driven by environment variables. Copy `.env.example` to `.env`
and fill in the required values before starting.

Generate strong secrets with:
```bash
openssl rand -hex 32
```

---

## Server

| Variable | Default | Description |
|---|---|---|
| `HOST` | `0.0.0.0` | Bind address for the HTTP/HTTPS server |
| `PORT` | `8080` | Listen port |
| `GIN_MODE` | `release` | Gin mode: `debug` \| `release` (debug logs request bodies) |
| `TLS_CERT_FILE` | | Path to PEM TLS certificate chain. Both this and `TLS_KEY_FILE` must be set to enable HTTPS. |
| `TLS_KEY_FILE` | | Path to PEM TLS private key |

## Rate Limiting

| Variable | Default | Description |
|---|---|---|
| `RATE_LIMIT_PER_MIN` | `120` | Max requests per minute per client IP on authenticated routes. `0` = disabled. |
| `ACTOR_RATE_LIMIT_PER_MIN` | `0` | Max requests per minute per API key actor. `0` = inherit `RATE_LIMIT_PER_MIN`; `-1` = disabled. |
| `CORS_ALLOWED_ORIGINS` | | Comma-separated list of allowed CORS origins. Empty = block all cross-origin requests (recommended for API-only use). |

---

## Database (PostgreSQL)

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://vaultrun:vaultrun@localhost:5432/vaultrun?sslmode=disable` | PostgreSQL connection string (libpq DSN format) |
| `DB_MAX_OPEN_CONNS` | `25` | Maximum number of open connections in the pool |
| `DB_MAX_IDLE_CONNS` | `5` | Maximum number of idle connections in the pool |

### PostgreSQL TLS

The four `DB_SSL_*` variables override any SSL parameters already embedded in
`DATABASE_URL`. At minimum, set `DB_SSL_MODE` — the rest are only required for
mutual TLS or self-signed CA validation.

| Variable | Default | Description |
|---|---|---|
| `DB_SSL_MODE` | | Encryption mode for the Postgres connection. See modes below. |
| `DB_SSL_ROOT_CERT` | | Path to the PostgreSQL server CA certificate file (PEM). Required for `verify-ca` and `verify-full`. |
| `DB_SSL_CERT` | | Path to the client certificate file (PEM). Required for mutual TLS. |
| `DB_SSL_KEY` | | Path to the client private key file (PEM). Required for mutual TLS. |

**`DB_SSL_MODE` values:**

| Mode | Encrypted | Cert verified | Use when |
|---|---|---|---|
| `disable` | No | — | Local dev only |
| `allow` | Maybe | No | Not recommended |
| `prefer` | When possible | No | Default if unset (no hard guarantee) |
| `require` | Yes | No | Traffic is encrypted but server identity is not verified |
| `verify-ca` | Yes | CA checked | Server cert signed by a trusted CA |
| `verify-full` | Yes | CA + hostname | **Production recommendation** |

> **Warning**: VaultRun emits a startup warning whenever `sslmode=disable` is
> detected in the effective DSN. This is expected in local development but should
> be resolved before any deployment that processes real data.

**Production example:**
```bash
DATABASE_URL=postgres://vaultrun:password@postgres:5432/vaultrun
DB_SSL_MODE=verify-full
DB_SSL_ROOT_CERT=/run/secrets/postgres-ca.pem
DB_SSL_CERT=/run/secrets/api-client.pem
DB_SSL_KEY=/run/secrets/api-client-key.pem
```

---

## Redis

| Variable | Default | Description |
|---|---|---|
| `REDIS_ADDR` | `localhost:6379` | Redis server address (`host:port`) |
| `REDIS_PASSWORD` | | Redis `AUTH` password. Required for any non-localhost Redis. |
| `REDIS_DB` | `0` | Redis logical database (0–15) |
| `ASYNC_WORKERS` | `4` | Number of concurrent job consumer goroutines |
| `ASYNC_QUEUE_SIZE` | `256` | In-memory queue depth (only applies when `REDIS_ADDR` is not set) |

When `REDIS_ADDR` is set, the durable **Redis Streams** backend is used and
async jobs survive server restarts. Without it, an in-process bounded channel
is used (jobs are lost on restart).

---

## Docker Sandbox

| Variable | Default | Description |
|---|---|---|
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker daemon address |
| `DOCKER_CONTAINER_PREFIX` | `vaultrun` | Prefix for container names |
| `DOCKER_DEFAULT_IMAGE` | `python:3.12-slim` | Default container image when a session doesn't specify one |
| `DOCKER_IDLE_TIMEOUT_MINS` | `30` | Minutes of inactivity before a session container is stopped (workspace preserved) |
| `DOCKER_IMAGE_ALLOWLIST` | | Comma-separated list of permitted container images. Empty = allow any image. **Restrict this in production.** |

### Seccomp

| Variable | Default | Description |
|---|---|---|
| `DOCKER_SECCOMP_PROFILE` | | Controls the seccomp syscall filter applied to every container. |

| Value | Effect |
|---|---|
| *(empty, default)* | Use the **embedded vaultrun profile** compiled into the binary — recommended for production |
| `default` | Pass `seccomp=default` explicitly to use the Docker daemon's built-in filter |
| `/absolute/path` | Load and embed a custom JSON seccomp profile from the given file |

The embedded profile (sourced from `deployments/seccomp/vaultrun-seccomp.json`)
is compiled into the binary via `//go:embed` so the binary is fully self-contained
and does not depend on external files or the daemon's default filter.

### Cosign Image Signature Verification

| Variable | Default | Description |
|---|---|---|
| `COSIGN_PUBLIC_KEY` | | Path to a cosign PEM public key file. When set, `CreateSandbox` verifies every image with `cosign verify --key <file> <image>` before use. **Fail-closed**: if the binary is absent but the key is set, sandbox creation is rejected. |

**Setup:**
```bash
# 1. Generate a key pair
cosign generate-key-pair   # → cosign.key (private), cosign.pub (public)

# 2. Sign your images on push
cosign sign --key cosign.key docker.io/yourorg/image:tag

# 3. Configure VaultRun
COSIGN_PUBLIC_KEY=/run/secrets/cosign.pub
```

### Network Egress Filtering

When a session is created with `allowed_hosts` set **and** `network_enabled: true`,
VaultRun creates a dedicated per-session Docker bridge network and installs
host-side iptables rules that allow only:
- DNS (UDP/TCP port 53)
- Outbound connections to the resolved IPs of the listed hosts
- ESTABLISHED/RELATED reply traffic

All other egress is dropped. The API server process requires `CAP_NET_ADMIN`
(see `cap_add: [NET_ADMIN]` in `deployments/docker-compose.yml`).

---

## Session Resource Limits

Hard caps applied at session creation time. Requests that exceed them are rejected
with HTTP 422.

| Variable | Default | Description |
|---|---|---|
| `MAX_SESSION_CPU` | `8` | Maximum fractional CPUs per session (e.g., `0.5` = half a core) |
| `MAX_SESSION_MEMORY_MB` | `8192` | Maximum RAM in megabytes per session |
| `MAX_SESSION_TIMEOUT_SEC` | `86400` | Maximum run `timeout_seconds` (86400 = 24 h) |
| `MAX_SESSIONS_PER_ACTOR` | `20` | Maximum concurrent sessions per API key actor. `0` = unlimited. |

---

## Workspace

| Variable | Default | Description |
|---|---|---|
| `WORKSPACE_BASE_DIR` | `/data/workspaces` | Host directory for session workspaces, bind-mounted into containers at `/workspace`. Must be writable by the API process. |
| `MAX_FILE_MB` | `100` | Maximum single upload file size in megabytes |
| `MAX_WORKSPACE_MB` | `0` | Maximum total workspace size per session in megabytes. `0` = unlimited. Enforced at upload time — artifact writes by container runs are not capped here. |
| `MAX_OUTPUT_MB` | `10` | Maximum run stdout+stderr capture (output is truncated beyond this; the run still completes) |

---

## Authentication

| Variable | Default | Description |
|---|---|---|
| `MASTER_API_KEY` | | Bootstrap key for `POST /api/v1/keys`. Used only to create the first API key. **Rotate or clear after bootstrapping.** Generate with `openssl rand -hex 32`. |
| `OPA_POLICY_FILE` | | Path to an OPA/Rego policy file for command-level allow/deny decisions. Empty = AllowAll (every command permitted). |

---

## SSO — OIDC / OpenID Connect

> **For a step-by-step walkthrough** — registering an application with your
> IdP, generating certificates, testing the login flow, and troubleshooting —
> see [docs/sso-setup.md](sso-setup.md). The tables below are a quick
> environment-variable reference.

OIDC is enabled when `OIDC_ISSUER_URL` is set together with the client credentials.
After a successful OIDC login the server maps the user's `sub` claim to a VaultRun API
key and sets a signed session cookie (`vaultrun_session`). Existing API key auth is
unaffected.

| Variable | Default | Description |
|---|---|---|
| `OIDC_ISSUER_URL` | | IdP issuer URL — discovery is performed at `{issuer}/.well-known/openid-configuration` |
| `OIDC_CLIENT_ID` | | OAuth2 client ID registered with the IdP |
| `OIDC_CLIENT_SECRET` | | OAuth2 client secret |
| `OIDC_REDIRECT_URL` | | Callback URL registered with the IdP (e.g. `https://api.example.com/auth/oidc/callback`) |
| `OIDC_SCOPES` | `openid,email,profile` | Comma-separated OIDC scopes |

**Supported IdPs:** Okta · Azure AD · Google Workspace · Keycloak · Auth0 · any OIDC-compliant IdP

**OIDC flow:**
1. `GET /auth/oidc/login` → browser redirects to IdP (PKCE + state cookie)
2. IdP calls back at `GET /auth/oidc/callback?code=…&state=…`
3. Server exchanges code for ID token, upserts `sso_users` row, sets session cookie
4. User is redirected to `/`

---

## SSO — SAML 2.0

SAML is enabled when `SAML_IDP_METADATA_URL`, `SAML_CERT_FILE`, and `SAML_KEY_FILE` are all set.

| Variable | Default | Description |
|---|---|---|
| `SAML_IDP_METADATA_URL` | | URL of the IdP's SAML metadata XML |
| `SAML_CERT_FILE` | | Path to PEM certificate for this Service Provider |
| `SAML_KEY_FILE` | | Path to PEM private key for this Service Provider |
| `SAML_ROOT_URL` | | Public base URL of the API server (e.g. `https://api.example.com`) |
| `SAML_ENTITY_ID` | `SAML_ROOT_URL/auth/saml/metadata` | SP entity ID; must match what is registered in the IdP |

**Generate a self-signed SP certificate:**
```bash
openssl req -x509 -newkey rsa:2048 -keyout saml.key -out saml.crt -days 3650 -nodes \
  -subj "/CN=vaultrun-sp"
SAML_CERT_FILE=/path/to/saml.crt
SAML_KEY_FILE=/path/to/saml.key
```

**SAML routes:**
| Route | Description |
|---|---|
| `GET /auth/saml/metadata` | SP metadata XML — give this URL to your IdP during setup |
| `GET /auth/saml/login` | Redirects browser to IdP SSO URL |
| `POST /auth/saml/acs` | Assertion Consumer Service — receives the IdP's assertion after login |

---

## SSO — Session Cookie

| Variable | Default | Description |
|---|---|---|
| `SSO_SESSION_SECRET` | **required when SSO is enabled** | HS256 signing key for session JWTs. **Required when OIDC or SAML is enabled.** Generate with `openssl rand -hex 32`. |
| `SSO_SESSION_MAX_AGE_HOURS` | `24` | Session lifetime in hours |
| `SSO_SESSION_SECURE` | `true` when TLS is active | Set `Secure` flag on the session cookie. Set `false` only in local dev without TLS. |

**Additional SSO routes (require active session):**
| Route | Description |
|---|---|
| `GET /auth/me` | Returns the authenticated SSO user's profile |
| `POST /auth/logout` | Clears the session cookie |

---

## Multi-Region

| Variable | Default | Description |
|---|---|---|
| `REGION` | | Region identifier included in `/health` responses (e.g. `us-east-1`). Optional but recommended in multi-region deployments. |
| `DATABASE_READ_URL` | | DSN of a PostgreSQL read replica. When set, read-heavy queries (list/get) are routed here; write queries always go to the primary `DATABASE_URL`. Format matches `DATABASE_URL`. |

See [`docs/multi-region.md`](multi-region.md) for full deployment guides.

| Variable | Default | Description |
|---|---|---|
| `LOG_LEVEL` | `info` | Log verbosity: `debug` \| `info` \| `warn` \| `error` |
| `WEBHOOK_SECRET` | | HMAC-SHA256 signing key for async run callback payloads. Sent as `X-VaultRun-Signature: sha256=<hex>`. Empty = no signing (insecure for internet-facing callbacks). |
| `STOP_CONTAINERS_ON_SHUTDOWN` | `false` | When `true`, gracefully stop all running session containers on SIGTERM. |
| `AUDIT_LOG_RETENTION_DAYS` | `90` | Delete audit log entries older than N days on each cleanup sweep. `0` = retain indefinitely. |

Prometheus metrics are exposed at `GET /metrics`. The most important metrics:

| Metric | Type | Description |
|---|---|---|
| `vaultrun_container_creations_total` | Counter | Containers created/failed |
| `vaultrun_container_stops_total` | Counter | Containers stopped |
| `vaultrun_run_duration_ms` | Histogram | Run execution duration |
| `vaultrun_run_status_total` | Counter | Runs by status (completed/failed/timeout) |
| `vaultrun_active_sessions` | Gauge | Current active session count |
| `vaultrun_job_queue_depth` | Gauge | Current async job queue depth |

---

## Migrations

| Variable | Default | Description |
|---|---|---|
| `MIGRATIONS_PATH` | `migrations` | Path to the SQL migration files directory (relative to working directory) |
