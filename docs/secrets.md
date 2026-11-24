# Secrets Broker

VaultRun supports four secrets backends, selected via `SECRETS_PROVIDER`.
Secrets are resolved at run-time and injected as environment variables into
container exec calls. The secret values themselves are not stored in the
database or logged, and their exact values are redacted from command output
(see [Output redaction](#output-redaction) for what that does and does not cover).

## How it works

When a caller submits a run request with a `secrets` array:

```json
POST /api/v1/sessions/:id/run
{
  "command": "python",
  "args": ["script.py"],
  "secrets": ["DB_PASSWORD", "API_KEY"]
}
```

VaultRun resolves each name via the configured provider and merges the
values into `env` before the container exec. They exist in the container's
process environment; the caller does not receive them in the run output (see
below).

### Which keys may request which secrets

By default an API key can request any secret the provider can resolve (the
behaviour before v0.4.1). Restrict a key with an allowlist, set when the key
is created or changed later (master key only):

```bash
# create a key that can only inject DB_PASSWORD
curl -X POST $API/api/v1/keys -H "Authorization: Bearer $MASTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci","allowed_secrets":["DB_PASSWORD"]}'

# change it later: a list restricts, [] denies all, null removes the restriction
curl -X PUT $API/api/v1/keys/$KEY_ID/secrets -H "Authorization: Bearer $MASTER_API_KEY" \
  -H 'Content-Type: application/json' -d '{"allowed_secrets":[]}'
```

A run that asks for a secret outside the allowlist fails with `403` before any
lookup happens, so the response does not reveal whether the secret exists, and
a `secret.denied` audit entry is written. The master key is never restricted.
Keys created before migration 022 stay unrestricted until you set a list.

### Output redaction

For every run that requests `secrets`, VaultRun replaces the exact value of
each injected secret with `[REDACTED:<NAME>]` in stdout and stderr before the
output is stored in the database, returned by the API or streamed over SSE
(sync, async and `/run/stream`). The base64 forms (standard and URL-safe,
padded or not) are redacted too, so `echo $SECRET | base64` does not leak.

Limits you should know about:

- Only secrets requested in that run are redacted, and only values of at least
  4 characters (shorter values would mangle normal output).
- It is an exact-match safety net against accidental leaks, not a defence
  against a command that wants to exfiltrate the value: hex, reversed or
  split output is not caught. Treat a command that receives a secret as
  trusted with it.
- Files the command writes to `/workspace` are not scanned.
- If output hits the size limit in the middle of a secret, the truncated
  prefix is not redacted.

---

## Provider: `env` (default)

Reads secrets from host environment variables with the prefix
`VAULTRUN_SECRET_`.

```
SECRETS_PROVIDER=env
VAULTRUN_SECRET_DB_PASSWORD=hunter2
VAULTRUN_SECRET_API_KEY=sk-...
```

Secret name `"DB_PASSWORD"` maps to env var `VAULTRUN_SECRET_DB_PASSWORD`.
Names are uppercased before lookup.

**Docker Compose:** the `api` service loads `.env` (`env_file`), so adding the
variables there and re-running `make up` is enough. Entries in `.env` end up in
the API container's environment; keep the file out of version control.

**When to use:** development, single-host deployments.

---

## Provider: `vault` (HashiCorp Vault KV v2)

```
SECRETS_PROVIDER=vault
VAULT_ADDR=https://vault.internal:8200
VAULT_TOKEN=                  # static token; leave empty to use AppRole below
VAULT_ROLE_ID=
VAULT_SECRET_ID=
VAULT_NAMESPACE=              # optional X-Vault-Namespace on login and reads
VAULT_MOUNT=secret            # KV v2 mount (default: secret)
VAULT_PATH=vaultrun           # path prefix under the mount
VAULT_FIELD=                  # optional; default is "value", then the secret name
VAULT_VERSION=                # optional KV v2 version (digits only)
```

Secrets are read from `{VAULT_ADDR}/v1/{VAULT_MOUNT}/data/{VAULT_PATH}/{name}`.
`VAULT_VERSION` is sent as `?version=`. `VAULT_FIELD` selects one field and
does not fall back to another name.

Example — store the secret:
```
vault kv put secret/vaultrun/DB_PASSWORD value=hunter2
```

Request it with `"secrets": ["DB_PASSWORD"]`.

### Production recommendations

1. **AppRole auth** instead of a root token:
   ```
   vault auth enable approle
   vault write auth/approle/role/vaultrun \
     token_policies=vaultrun-policy \
     token_ttl=1h token_max_ttl=4h
   ```
   Then set `VAULT_ROLE_ID` and `VAULT_SECRET_ID` and leave `VAULT_TOKEN`
   empty. The API logs in with AppRole, caches the client token, and
   refreshes it before the lease ends. A set `VAULT_TOKEN` is used as-is
   and skips the login. The role id, secret id, and client token are not
   logged.

2. **Namespace isolation:** use a dedicated KV mount per environment
   (`secret-prod/`, `secret-staging/`).

3. **Vault HA:** run Vault in HA mode (Raft or Consul storage) so a single
   Vault node failure does not block secret resolution.

---

## Provider: `aws` (AWS Secrets Manager)

```
SECRETS_PROVIDER=aws
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
AWS_SESSION_TOKEN=              # optional, for temporary credentials
SECRETS_AWS_PREFIX=vaultrun/    # secret name prefix in Secrets Manager
```

Secret name `"DB_PASSWORD"` maps to Secrets Manager secret
`vaultrun/DB_PASSWORD`. The value must be a JSON object with a `"value"`
key **or** a plain string:

```bash
aws secretsmanager create-secret \
  --name vaultrun/DB_PASSWORD \
  --secret-string '{"value":"hunter2"}'
```

Uses SigV4-signed HTTP — **no AWS SDK dependency**.

### Production recommendations

1. **IAM role instead of access keys:** attach an IAM role with
   `secretsmanager:GetSecretValue` on `arn:aws:secretsmanager:REGION:ACCOUNT:secret:vaultrun/*`
   to the EC2 instance or ECS task. Leave `AWS_ACCESS_KEY_ID` and
   `AWS_SECRET_ACCESS_KEY` unset. The API process then uses IMDSv2: it
   requests a metadata token and reads the role credentials. Environment
   keys win when they are set. If `AWS_REGION` and `AWS_DEFAULT_REGION`
   are also unset, the region comes from instance metadata. Sandbox
   egress still cannot reach the metadata address; only the API process
   talks to it.

2. **KMS encryption:** enable automatic KMS rotation on the secrets.

3. **Resource policy:** restrict who can read `vaultrun/*` secrets using
   a Secrets Manager resource policy.

---

## Provider: `azure` (Azure Key Vault)

```
SECRETS_PROVIDER=azure
AZURE_KEYVAULT_URL=https://myvault.vault.azure.net
# Service principal. Leave the secret empty to use the instance managed identity.
AZURE_TENANT_ID=
AZURE_CLIENT_ID=
AZURE_CLIENT_SECRET=
```

Secret name `"DB_PASSWORD"` is the latest version of that secret in the
vault (`GET /secrets/DB_PASSWORD?api-version=7.4`). The value is not stored.

A service principal uses client credentials against Entra, scope
`https://vault.azure.net/.default`. Without `AZURE_CLIENT_SECRET`, the API
process asks the instance metadata service for a token
(`resource=https://vault.azure.net`). `AZURE_CLIENT_ID` then selects a
user-assigned identity. The same rule as AWS applies: the sandbox cannot
reach the metadata address.

---

## What a key may request

`GET /api/v1/whoami` returns the caller's secret allowlist by name, never
the values, and never whether a name exists in the backend:

```json
{"actor":"ci","master":false,"secrets":{"unrestricted":false,"allowed":["DB_PASSWORD"]}}
```

`unrestricted` is true for the master key and for a key whose allowlist
was left unset. An empty `allowed` list means the key may not inject any
secret. The MCP tool `whoami` prints the same information. A session's
`allowed_hosts` (the destinations that session may reach) is on the
session, and `get_session` includes it.

---

## Rotating secrets

No restart is required — the provider fetches secrets on every run request.
Rotation in the backend (Vault lease renewal, a new AWS or Azure secret
version) is transparent to VaultRun. The AppRole client token is refreshed
on its own; that is separate from the secret value.

---

## Security notes

- Secret values are held in memory only for the duration of the exec call.
- Secrets are **not** included in audit log metadata.
- The `secrets` field in a run request is not stored in the `runs` table.
- Use TLS between VaultRun and the secrets backend in production.
