# VaultRun Security Model

## Threat Model

VaultRun is designed to safely execute untrusted AI agent code. The primary threats are:

1. **Code escaping its container** — malicious code breaking out of the sandbox
2. **Path traversal** — accessing files outside the session workspace
3. **Shell injection** — command arguments being misinterpreted as shell syntax
4. **Resource exhaustion** — a single agent consuming all host resources
5. **Unauthorized API access** — agents accessing other sessions' data
6. **Data exfiltration** — unauthorized network egress from sandboxed code
7. **Malicious images** — container images that bypass sandbox controls

## Mitigations

### Container Isolation

| Control | Implementation |
|---|---|
| No privileged containers | `Privileged: false` (Docker default + enforced) |
| Drop all capabilities | `CapDrop: ["ALL"]`, `CapAdd: []` |
| No new privileges | `SecurityOpt: ["no-new-privileges"]` |
| Non-root user | `User: "nobody"` in container config |
| Network disabled by default | `NetworkMode: "none"` unless explicitly enabled |
| No host network | Never uses `NetworkMode: "host"` |
| CPU + memory limits | NanoCPUs + hard memory limit (swap disabled) |
| PID limit | `PidsLimit: 512` — blocks fork-bomb attacks |
| Read-only root filesystem | `ReadonlyRootfs: true` — container image layers are immutable; prevents processes from modifying shared image state |
| Writable `/tmp` (tmpfs) | A 64 MB `tmpfs` is mounted at `/tmp`; the only writable location outside `/workspace`. Size-limited to prevent host memory exhaustion |
| Home directory isolation | `HOME=/tmp` — tools that write to `$HOME` (pip cache, Python bytecode, etc.) land in the ephemeral `/tmp` tmpfs, not the rootfs |
| No docker.sock exposure | The container never gets access to the Docker socket |
| Seccomp syscall filter | Embedded vaultrun profile applied at compile time via `//go:embed`; no external file dependency |

### Network Egress Filtering

When a session is created with `allowed_hosts` set and `network_enabled: true`:

1. A **dedicated per-session Docker bridge network** is created
2. Host-side **iptables rules** are applied to the bridge interface (`br-<networkID[:12]>`) via a custom chain (`vr-<containerID[:12]>`)
3. The chain allows: DNS (UDP/TCP 53), ESTABLISHED/RELATED replies, and IPs resolved from `allowed_hosts` — everything else is **dropped**
4. On session stop, rules and the Docker network are removed

Sessions without `allowed_hosts` (but with `network_enabled: true`) use the default bridge without iptables filtering. Sessions with `network_enabled: false` have no network at all.

### Image Signature Verification (cosign)

When `COSIGN_PUBLIC_KEY` is set, every `CreateSandbox` call:

1. Looks up the `cosign` binary on PATH (fails closed if absent)
2. Runs `cosign verify --key <file> <image>`
3. Rejects the sandbox creation if the signature is absent or invalid

This prevents unsigned or tampered images from being run even if they are cached locally.

### Command Execution

Shell injection is prevented by design:

```go
// The command and args are passed as SEPARATE fields to Docker exec API.
// They are NEVER concatenated into a shell command string.
ContainerExecCreate(ctx, containerID, types.ExecConfig{
    Cmd: append([]string{command}, args...),  // NO shell
    ...
})
```

The API also validates that the command string does not contain shell metacharacters
(`;`, `|`, `&`, `$`, `` ` ``, `\`, `<`, `>`, `{`, `}`, `(`, `)`).

Environment variable keys and values injected via the `env` field are validated to
reject null bytes, newlines, and `=` characters that could corrupt the Docker exec
environment string.

### Path Traversal Prevention

All user-provided file paths are sanitized before use:

```go
func (m *Manager) SafePath(sessionID uuid.UUID, userPath string) (string, error) {
    root := m.sessionPath(sessionID)
    cleaned := filepath.Clean("/" + userPath)   // Normalize
    resolved := filepath.Join(root, cleaned)     // Join with root

    // Verify the result is still inside root
    if !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
        return "", fmt.Errorf("path traversal detected")
    }
    return resolved, nil
}
```

This prevents `../../../etc/passwd`, URL-encoded variants, and other traversal patterns.

### API Authentication

- All API endpoints (except `/health` and `/metrics`) require an API key via `Authorization: Bearer <key>`
- Keys are stored as SHA-256 hashes — plaintext is never persisted
- Keys are generated with 32 bytes of cryptographic randomness
- A `master` key is available for bootstrapping but can be left empty after initial setup
- Key prefix (`vr_xxxx`) is stored for identification without exposing the secret
- Per-actor rate limiting prevents brute-force attacks on the key space

### Database TLS

The connection to PostgreSQL supports full TLS configuration:

| `DB_SSL_MODE` | Effect |
|---|---|
| `verify-full` | Full certificate verification (recommended for production) |
| `verify-ca` | CA chain verification (no hostname check) |
| `require` | Encrypted but server identity not verified |
| `disable` | No TLS (triggers startup warning; dev only) |

See [configuration.md](configuration.md) for the full list of `DB_SSL_*` variables.

### Output Size Limits

- Maximum output (stdout + stderr combined) per run: 10 MB (configurable via `MAX_OUTPUT_MB`)
- Maximum file upload size: 100 MB (configurable via `MAX_FILE_MB`)
- Excess output is truncated — the run still completes and `output_truncated: true` is set on the response

### Timeouts

- Each run has a configurable `timeout_seconds` (default: 30, max: 3600)
- Timeout is enforced via Go `context.WithTimeout` + Docker exec kill
- The API server itself has `ReadTimeout` (30s) and `WriteTimeout` (120s)

### Workspace Isolation

- Each session gets a unique UUID-named directory: `/data/workspaces/{session_id}/`
- No cross-session file access is possible at the filesystem level
- File metadata is stored per-session in Postgres
- Workspace directories are created with `0700` permissions (owner-only)

### Async Callback Security

Webhook callbacks (fired when async runs complete) include an `X-VaultRun-Signature`
header when `WEBHOOK_SECRET` is set:

```
X-VaultRun-Signature: sha256=<hmac-hex>
```

The HMAC-SHA256 signature is computed over the raw JSON request body using the
configured secret. Receivers should verify this before processing the payload.
Failed deliveries are retried with exponential backoff (up to 3 attempts).

### Audit Trail

Every security-relevant event generates an immutable audit log:

```
session.created    — who created what session with what parameters
session.deleted    — who deleted what session
session.expired    — idle timeout triggered by the cleanup service
file.uploaded      — who uploaded what file to which session
file.downloaded    — who downloaded what file from which session
command.started    — what command started in which session
command.finished   — result (exit code, duration)
command.failed     — failure reason
```

Audit logs have no update or delete endpoints.

---

## Remaining Limitations

| Limitation | Notes |
|---|---|
| No secret injection at the container level | Env vars in runs are stored as plaintext JSONB in Postgres. Use a secrets broker sidecar for sensitive values. |
| Single-node only | No distributed runner pool; horizontal scale requires sharding by session ID |
| Docker socket on host | The API process requires access to `/var/run/docker.sock` |
| No gVisor / microVM isolation | Container escape attacks are mitigated by seccomp + CapDrop but not eliminated; consider Firecracker for higher-risk workloads |
| OPA policy file only | The policy hook supports OPA/Rego; no UI for rule management |

## Recommendations for Production

1. Run the API server as a non-root user with only `CAP_NET_ADMIN` (for iptables egress filtering)
2. Enable TLS — either via `TLS_CERT_FILE`/`TLS_KEY_FILE` or a reverse proxy (nginx/caddy)
3. Set `DB_SSL_MODE=verify-full` with a CA certificate
4. Set `MASTER_API_KEY` to a cryptographically random value and clear it after bootstrapping
5. Set `DOCKER_IMAGE_ALLOWLIST` to restrict container images to your approved set
6. Set `COSIGN_PUBLIC_KEY` and sign all approved images — fail closed on unsigned images
7. Set `WEBHOOK_SECRET` for all async callback endpoints
8. Place the API behind a firewall; only expose port 8080/443 to trusted callers
9. Monitor audit logs for anomalous patterns (unusual commands, high file download rates)
10. Set up log rotation for workspace directories
11. Use the Redis Streams backend (`REDIS_ADDR`) for async runs in production
