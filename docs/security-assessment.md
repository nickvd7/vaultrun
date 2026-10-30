# External security assessment: brief

This is the brief for an independent penetration test of VaultRun. It gives a
tester what they need to scope, set up and start the work in one place. The
security model itself is in [security.md](security.md).

## What VaultRun is

A self-hosted runtime that lets AI agents run code, read and write files and
call services inside isolated containers. Users and agents reach it through a
REST API, an MCP server and a web dashboard. Customers run it on their own
infrastructure; there is no VaultRun cloud service.

The code that runs in a sandbox is **untrusted by design**: it is written by an
AI model or by whoever controls the agent. Assume the attacker can run any
code they like inside a sandbox, and holds a valid, non-admin API key.

## Goals

In order of priority:

1. **Escape the sandbox**: from code inside a sandbox, reach the host, the
   API process (its memory, environment, files), other sandboxes, or the
   network the host sits in.
2. **Break tenant isolation**: with one user's key or SSO session, read or
   change another user's sessions, files, runs, snapshots, artifacts,
   evidence or audit entries; escalate within an organisation.
3. **Become an administrator**: obtain the master key or an equivalent, by
   any route.
4. **Forge or hide evidence**: produce an evidence pack or audit chain that
   verifies but is false, or remove or alter entries without detection.
5. **Deny service**: exhaust the host's disk, memory, CPU, processes or
   database from a sandbox or with an API key, beyond the configured limits.

## In scope

| Component | Where |
|---|---|
| REST API (`/api/v1/*`, `/auth/*`, `/health`, `/metrics`) | `cmd/api` |
| Sandboxes: container configuration, gVisor, seccomp profile, egress filtering, workspace handling, run supervisor | `internal/docker`, `internal/workspace`, `internal/supervise` |
| Workspace quotas (watcher and XFS) | `internal/quota`, `internal/xfsquota` |
| Dashboard (Next.js) and its `/api`, `/auth` proxy | `apps/frontend` |
| MCP server, stdio and HTTP transport | `sdk/mcp` |
| SSO: OIDC and SAML (VaultRun Enterprise overlay) | `ee/sso`, `ee/api` (enterprise repository) |
| Evidence packs and RFC 3161 timestamps; audit chain | `internal/evidence`, `internal/audit` |
| Compliance report and license files (Enterprise) | `ee/report`, `ee/license` |
| CLI and SDKs, as clients that handle server responses | `cmd/cli`, `sdk/go`, `sdk/python` |

## Out of scope

- Volumetric denial of service against the network or the reverse proxy.
- Third-party services: the identity provider, the timestamp authority,
  container registries, cloud metadata itself.
- Social engineering and physical access.
- Findings that require the master key, root on the host or access to the
  Docker socket, unless they lead somewhere those should not.
- License enforcement as copy protection: license files are a reminder by
  design (see `ee/license`); forging one is only interesting when it breaks
  something else.

## Test environment

Run the tester's own instance from the release under test, configured the way
we recommend for production. On a Linux VM (4 vCPU, 8 GB, 40 GB disk, Docker
with Compose v2):

```bash
git clone https://github.com/nickvd7/vaultrun && cd vaultrun
cp .env.example .env
# remove VAULTRUN_INSECURE_DEV=true and set MASTER_API_KEY, METRICS_TOKEN,
# OPA_POLICY_FILE (or OPA_ALLOW_ALL=true), TLS_* or TLS_DISABLED=true behind a proxy
make harden        # gVisor + workspaces on XFS with project quotas
make up
make bootstrap-key
```

`make harden` sets `DOCKER_RUNTIME=runsc` and `WORKSPACE_QUOTA=xfs` (see
[production-hardening.md](production-hardening.md)). For SSO, build the enterprise
overlay (`make build-enterprise` in vaultrun-enterprise) and connect a test
tenant of an IdP (Keycloak is easiest); `docs/sso-setup.md` has the steps.

Run a second instance with the default runtime (`DOCKER_RUNTIME=runc`) as
well: a finding that only works without gVisor is still a finding, rated
lower.

### Accounts to create

With the master key (`POST /api/v1/keys`, `POST /api/v1/orgs`):

| Account | Purpose |
|---|---|
| Two regular keys, A and B, in no organisation | Cross-user access |
| An organisation with a viewer, an executor and an admin key | Role escalation |
| A key with `allowed_secrets` set, and a secret (`VAULTRUN_SECRET_TEST`) | Secret scoping |
| Two SSO users in different IdP groups, one mapped to an org role | SSO and role sync |

The master key stays with the tester's instance only; never test against a
customer's deployment.

## Where we would look first

- **Workspace file handling.** The sandbox controls its workspace; every API
  operation on it (download, upload, zip, snapshot create and restore,
  evidence hashing, artifact promotion) goes through `os.Root`. Races between
  the sandbox and the API (symlink swaps, hard links, FIFOs, very deep or
  wide trees) are the class of bug found before (see below).
- **Egress filtering.** iptables chains per session network (FORWARD and
  INPUT), DNS to the gateway only, IPv6 dropped. Try DNS rebinding,
  IPv4-mapped IPv6, the Docker embedded DNS, other containers on the host,
  `allowed_hosts` that resolve to private addresses later.
- **The run supervisor.** It is the API's own binary, mounted read-only into
  every sandbox and run as the sandbox user. Anything that lets a sandbox
  influence it, or the tool container that sets XFS quotas, is critical.
- **Session cookies on the API.** The API accepts the SSO cookie wherever it
  accepts a key; writes need the `X-VaultRun-Client` header (CSRF). Check
  every route, the WebSocket and the dashboard proxy.
- **Evidence packs.** Ed25519 over the exact bytes, an RFC 3161 token over
  the signature, intermediates fetched over AIA and stored in the pack. Try
  to make a pack verify with content or a time it should not have.
- **Audit chain.** HMAC chain with a signed head row; try deleting, reordering
  or truncating entries without `GET /audit/integrity` noticing.

## Already found and fixed

From our own testing, so the tester does not spend time on them. Each fix
has a test; a regression is a finding.

| Version | Finding | Severity |
|---|---|---|
| 0.4.13 | Symlink race in workspace reads let a sandbox read the API's `/proc/self/environ` (master key, database password, signing keys); snapshot restore wrote through symlinks | Critical |
| 0.4.13 | Sandboxes with network reached private ranges, cloud metadata and services on the host | High |
| 0.4.13 | `MAX_WORKSPACE_MB` applied to uploads only; a sandbox could fill the host's disk | Medium |
| 0.4.13 | Request bodies unbounded; Next.js with a critical advisory; no security headers on the dashboard | Medium |
| 0.4.13 | Timed-out commands kept running | Medium |
| 0.4.13 | SSO: logout reported success when the session could not be revoked; no domain/group restriction; e-mail used as audit actor | Medium |

## Rules of engagement

- Test only your own instance; no customer data is involved.
- Destructive tests (disk, memory, process exhaustion, kernel exploits) are
  allowed on the test VM.
- Report critical findings (sandbox escape, master key, cross-tenant access)
  as soon as they are confirmed, not at the end:
  `mail@030.dev` with `[SECURITY]` in the subject, or a private advisory on
  GitHub (see [SECURITY.md](../SECURITY.md)).

## What we would like back

- Each finding with a severity (CVSS 4.0), the affected version, steps or a
  script to reproduce, and the impact in VaultRun's terms (which of the goals
  above it reaches).
- What was tested and found sound, so the report is useful as evidence for
  customers.
- A retest of the fixes.

A reasonable first engagement is 8–12 tester-days: sandbox and host isolation
take the most time. Look for testers with container-escape and Linux-kernel
experience (CREST, OSCP/OSCE or equivalent), not only web-application testing.
