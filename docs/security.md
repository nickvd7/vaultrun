# VaultRun Security Model

## Threat Model

VaultRun is designed to safely execute untrusted AI agent code. The primary threats are:

1. **Code escaping its container** — malicious code breaking out of the sandbox
2. **Path traversal** — accessing files outside the session workspace
3. **Shell injection** — command arguments being misinterpreted as shell syntax
4. **Resource exhaustion** — a single agent consuming all host resources
5. **Unauthorized API access** — agents accessing other sessions' data
6. **Data exfiltration** — unauthorized network egress from sandboxed code

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
| Resource limits | CPU via NanoCPUs, Memory via hard limit |
| No docker.sock exposure | The container never gets access to the Docker socket |
| Readonly root filesystem | Workspace is bind-mounted; container root is not writable |

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

Additionally, the API validates that the command string does not contain shell metacharacters (`;`, `|`, `&`, `$`, etc.).

### Path Traversal Prevention

All user-provided file paths are sanitized before use:

```go
func (m *Manager) SafePath(sessionID uuid.UUID, userPath string) (string, error) {
    root := m.sessionPath(sessionID)
    cleaned := filepath.Clean("/" + userPath)      // Normalize
    resolved := filepath.Join(root, cleaned)        // Join with root

    // Verify the result is still inside root
    if !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
        return "", fmt.Errorf("path traversal detected")
    }
    return resolved, nil
}
```

This prevents:
- `../../../etc/passwd`
- `./../../etc/hosts`
- URL-encoded variants (handled by HTTP layer before reaching this code)

### API Authentication

- All API endpoints (except `/health`) require an API key
- Keys are stored as SHA-256 hashes — plaintext is never persisted
- Keys are generated with 32 bytes of cryptographic randomness
- A `master` key is available for bootstrapping but can be left empty after initial setup
- Key prefix (`vr_xxxx`) is stored for identification without exposing the secret

### Output Size Limits

- Maximum output (stdout + stderr combined) per run: 10 MB (configurable)
- Maximum file upload size: 100 MB (configurable)
- Excess output is silently truncated — the run still completes

### Timeouts

- Each run has a configurable `timeout_seconds` (default: 30, max: 3600)
- Timeout is enforced via Go `context.WithTimeout` + Docker exec kill
- The API server itself has `ReadTimeout` and `WriteTimeout`

### Workspace Isolation

- Each session gets a unique UUID-named directory: `/data/workspaces/{session_id}/`
- No cross-session file access is possible at the filesystem level
- File metadata is stored per-session in Postgres

### Audit Trail

Every security-relevant event generates an audit log:

```
session.created    — who created what session with what parameters
session.deleted    — who deleted what session
file.uploaded      — who uploaded what file to which session
file.downloaded    — who downloaded what file from which session
command.started    — what command started in which session
command.finished   — result (exit code, duration)
command.failed     — failure reason
```

Audit logs are append-only (no update/delete endpoints).

## Known Limitations (MVP)

1. **No gVisor/seccomp profiles** — container kernel call filtering not yet applied
2. **No per-session network policies** — network is all-or-nothing
3. **No secret injection** — environment variables from the API are in plaintext in the DB
4. **Single-node only** — no distributed runner pool yet
5. **Docker socket on host** — the API process needs Docker socket access
6. **No image allowlist** — any image can be specified; add validation for production

## Recommendations for Production

1. Run the API server as a non-root user
2. Use TLS (reverse proxy with nginx/caddy)
3. Set `MASTER_API_KEY` to a cryptographically random value and rotate it after bootstrap
4. Apply seccomp profiles to Docker containers
5. Enable Docker Content Trust for image verification
6. Place the API behind a firewall; only expose port 8080 to trusted callers
7. Monitor audit logs for anomalous patterns
8. Set up log rotation for workspace directories
9. Consider adding an image allowlist to the policy hook
