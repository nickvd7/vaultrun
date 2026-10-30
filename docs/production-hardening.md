# Production hardening checklist

The defaults are tuned for a local quickstart: `make up` and it works. Several of them are too open for a deployment that untrusted clients can reach. This page lists what to change and what VaultRun can enforce for you.

## Secure by default

The API refuses to start unless all of these hold:

| Check | Satisfy it with | Explicit opt-out |
|-------|-----------------|------------------|
| OPA policy | `OPA_POLICY_FILE=/path/policy.rego` | `OPA_ALLOW_ALL=true` (accept AllowAll on purpose) |
| TLS | `TLS_CERT_FILE` + `TLS_KEY_FILE`, or `ACME_DOMAIN` | `TLS_DISABLED=true` (a reverse proxy terminates TLS) |
| `/metrics` auth | `METRICS_TOKEN=<random>` (bearer token). Without it `/metrics` still requires the master API key | none |
| Master key | if set, at least 32 characters (`openssl rand -hex 32`); empty is allowed once you have bootstrapped | none |
| Audit signing | `AUDIT_HMAC_KEY=<random>` | none |
| Redis password | `REDIS_PASSWORD`, checked only when `REDIS_ADDR` is set | none |
| Kernel isolation | `DOCKER_RUNTIME=runsc` (gVisor; `make gvisor` installs it on Linux) | `DOCKER_RUNTIME=runc` (accept the shared host kernel) |

Every failed check is logged as `insecure configuration` with the variable to set.

### Local development

`.env.example` contains `VAULTRUN_INSECURE_DEV=true`, so `cp .env.example .env && make up` works with open defaults. With it set, the API starts anyway and logs one warning listing what is open. **Remove that line for any deployment other people can reach.**

If you start the API without that variable (your own `.env`, the release binary, your own compose file) and the checks fail, it exits with the list of problems instead of running open.

Upgrading from 0.3.4 or earlier: an existing `.env` without `VAULTRUN_INSECURE_DEV` now has to either pass the checks or add that line. `make up` prints a note when the line is missing, and `make dev-env` adds it for local use. Check the cause with `make logs`: the API logs each `insecure configuration` problem before exiting.

## Not covered by the startup check

These need action on your side:

- **Workspace permissions.** Sandboxes run as the same uid:gid as the API process (65532 under Compose) and own their session directory, which is created `0700`, so no other host user or container can read it. The parent `WORKSPACE_BASE_DIR` is `0700` as well. Only when the API itself runs as root, or when you set `SANDBOX_USER` to another user (for example `nobody`), does VaultRun fall back to world-writable (`0777`) session directories, because that user is "other" to the directory owner; the `0700` parent then limits who can reach them. Keep the host single-tenant and do not mount the workspace directory anywhere else. Directories created by v0.4.6 and earlier stay `0777` until the session is deleted (the parent is `0700`, so they are not reachable by other users). Details: `internal/sandboxuser/sandboxuser.go`.
- **Docker socket.** Access to `/var/run/docker.sock` is root on the host. Under Compose only the `docker-guard` service has it; the API gets a filtered Docker API that refuses privileged containers, host mounts and commands in other containers ([security.md](security.md#docker-socket-the-docker-guard)). The guard itself is root-equivalent, so still run VaultRun on a dedicated host or VM, with nothing else on it. If you run the API binary outside Compose, run `vaultrun-api docker-guard` next to it and point `DOCKER_HOST` at its socket; the API logs a warning at startup when it talks to the daemon directly.
- **Availability and backups.** VaultRun runs on one host and has no failover. Back up nightly with `make backup` and test a restore; see [operations.md](operations.md) for what is lost when the host fails and how to recover.
- **Kernel isolation.** Covered by the startup check above: install gVisor with `make gvisor` (pinned release, checksum verified, registered with Docker without a restart) and `make up` sets `DOCKER_RUNTIME=runsc`. The API refuses to start if Docker does not know the runtime. Everything (run timeouts, network filtering, workspace quotas, evidence) is tested under gVisor in CI. See [configuration.md](configuration.md#sandbox-runtime-gvisor).
- **Image allowlist.** Set `DOCKER_IMAGE_ALLOWLIST` to the images you approve, and `COSIGN_PUBLIC_KEY` to fail closed on unsigned ones.
- **Database TLS.** Set `DB_SSL_MODE=verify-full` with `DB_SSL_ROOT_CERT`.
- **Network exposure.** Bind to a private interface or put the API behind a firewall; only expose 443 to trusted callers. The compose file already binds to `127.0.0.1`.
- **Webhooks.** Set `WEBHOOK_SECRET` for async callbacks.
- **Workspace size.** Set `MAX_WORKSPACE_MB` (e.g. `2048`). With `0` a sandbox can fill the host's disk, and Postgres with it. By default a sandbox over the cap is paused within seconds; for a hard limit with no overshoot, use XFS project quotas:
  ```bash
  make xfs-workspace                      # image file of 50G (XFS_SIZE=200G for more)
  XFS_DEVICE=/dev/sdb make xfs-workspace  # or: format a dedicated disk (erases it)
  make up
  ```
  It creates the filesystem, mounts it on `WORKSPACE_HOST_DIR` with `prjquota`, adds it to `/etc/fstab` (mounted before Docker at boot), and sets `WORKSPACE_QUOTA=xfs` (and `MAX_WORKSPACE_MB=2048` if it was 0) in `.env`. The directory must be empty: run `make down-all` first on an existing install. If the kernel cannot mount XFS with project quotas, it stops and changes nothing. `make harden` runs `make gvisor` and `make xfs-workspace` together.
  Every new session directory then gets a project of its own limited to `MAX_WORKSPACE_MB`; a write past it fails in the sandbox with "No space left on device". The docker guard sets it from a one-shot container with `CAP_SYS_ADMIN` and its own binary; the API refuses to start when the directory has no project quotas. Sessions created before keep the paused-when-over behaviour.
- **Run timeouts.** A command that runs past its `timeout_seconds` is stopped, with the processes it started, by a supervisor the API mounts read-only into every sandbox (`/.vaultrun/vr-exec`). Background processes of a command that finishes in time keep running; a process that detaches on purpose (`setsid`) is not stopped and stays bounded by the sandbox's CPU, memory and process limits until the session ends. Sandboxes created before this version run commands without it until they are recreated.
- **Sandbox egress.** Sandboxes with `network_enabled` cannot reach private ranges, cloud metadata or the host. Keep `SANDBOX_ALLOW_PRIVATE_NETWORKS` unset unless sandboxes must reach internal services, and then prefer `allowed_hosts`. This needs iptables (the Compose `netpolicy` helper); the API logs a warning per session when it cannot filter.
- **Request size.** Bodies are capped at 10 MB, uploads at `MAX_FILE_MB` + 1 MB. Put a reverse proxy with its own limits and timeouts in front as well.

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
DOCKER_RUNTIME=runsc
AUDIT_HMAC_KEY=<openssl rand -hex 32>
MAX_WORKSPACE_MB=2048
```
