# Secrets Broker

VaultRun supports three secrets backends, selected via `SECRETS_PROVIDER`.
Secrets are resolved at run-time and injected as environment variables into
container exec calls — they are **never stored in the database** or logged.

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
values into `env` before the container exec. The caller never sees the
values; they exist only in the container's process environment.

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

**When to use:** development, single-host deployments.

---

## Provider: `vault` (HashiCorp Vault KV v2)

```
SECRETS_PROVIDER=vault
VAULT_ADDR=https://vault.internal:8200
VAULT_TOKEN=s.xxxxxx          # or use VAULT_ROLE_ID + VAULT_SECRET_ID for AppRole
VAULT_MOUNT=secret            # KV v2 mount (default: secret)
VAULT_PATH=vaultrun           # path prefix under the mount
```

Secrets are read from `{VAULT_ADDR}/v1/{VAULT_MOUNT}/data/{VAULT_PATH}/{name}`.

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
   Then set `VAULT_ROLE_ID` / `VAULT_SECRET_ID` (the provider reads these
   automatically when `VAULT_TOKEN` is empty — _future work: add AppRole
   login to the provider_; for now, exchange credentials for a token in your
   init container or startup script).

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
   to the EC2 instance / ECS task / EKS service account. Leave
   `AWS_ACCESS_KEY_ID` unset — the provider will use the instance metadata
   credentials automatically (_note: the current provider reads
   `AWS_ACCESS_KEY_ID` from env; for IMDSv2 support, use an init container
   that exports credentials into env vars_).

2. **KMS encryption:** enable automatic KMS rotation on the secrets.

3. **Resource policy:** restrict who can read `vaultrun/*` secrets using
   a Secrets Manager resource policy.

---

## Rotating secrets

No restart is required — the provider fetches secrets on every run request.
Rotation in the backend (Vault lease renewal, AWS secret version rotation)
is transparent to VaultRun.

---

## Security notes

- Secret values are held in memory only for the duration of the exec call.
- Secrets are **not** included in audit log metadata.
- The `secrets` field in a run request is not stored in the `runs` table.
- Use TLS between VaultRun and the secrets backend in production.
