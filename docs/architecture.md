# VaultRun Architecture

## Overview

VaultRun is a self-hosted secure runtime for AI agents. It provides isolated execution environments (Docker containers) that agents use to safely run code, manipulate files, and produce artifacts — all on your own infrastructure.

```
┌─────────────────────────────────────────────────────────────────┐
│                         User / Agent                            │
│              (CLI / SDK / Dashboard / API direct)               │
└───────────────────────────┬─────────────────────────────────────┘
                            │ HTTPS + API Key
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                        API Server (Go + Gin)                    │
│                                                                 │
│   POST /sessions   GET /sessions/:id    DELETE /sessions/:id   │
│   POST /sessions/:id/run                                        │
│   POST /sessions/:id/files  GET /sessions/:id/files/*path      │
│   GET  /audit                                                   │
│                                                                 │
│   ┌─────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│   │  Auth MW    │  │  Audit Log   │  │  Policy Hook (noop)  │  │
│   └─────────────┘  └──────────────┘  └──────────────────────┘  │
└──────┬──────────────────────┬──────────────────────────────────┘
       │                      │
       ▼                      ▼
┌──────────────┐    ┌──────────────────────────────────────────┐
│  PostgreSQL  │    │         Docker Runtime                   │
│  - sessions  │    │                                          │
│  - runs      │    │   Session A Container                    │
│  - files     │    │   ┌────────────────────────────────┐     │
│  - audit_logs│    │   │ python:3.12-slim               │     │
│  - api_keys  │    │   │ User: nobody                   │     │
└──────────────┘    │   │ Network: none                  │     │
                    │   │ Workspace: /data/workspaces/   │     │
┌──────────────┐    │   │          {session_id}/         │     │
│   Redis      │    │   │ CPU: 1 core  Memory: 512MB     │     │
│  (future)    │    │   └────────────────────────────────┘     │
└──────────────┘    │                                          │
                    │   Session B Container                    │
                    │   ┌────────────────────────────────┐     │
                    │   │ node:20-slim  ...               │     │
                    │   └────────────────────────────────┘     │
                    └──────────────────────────────────────────┘
```

## Core Concepts

### Session
A session is a long-lived sandbox environment. It consists of:
- A Docker container running the specified image
- An isolated workspace directory on the host filesystem (`/data/workspaces/{session_id}/`)
- Metadata stored in Postgres (status, resource limits, etc.)

Sessions persist until explicitly deleted. The container runs `sleep infinity` and commands are executed via `docker exec`.

### Run
A run is a single command execution within a session. Each run:
- Is executed via the Docker exec API (not via a shell — preventing injection)
- Has configurable timeout enforcement
- Captures stdout/stderr up to a maximum size
- Persists input + output metadata in Postgres
- Generates audit events

### Workspace
Each session gets an isolated directory on the host that is bind-mounted into the container at `/workspace`. This allows:
- Files to persist across multiple runs within a session
- The API to read/write files without network calls to the container
- Safe path validation to prevent traversal attacks

### Artifact
Any file produced inside `/workspace` during a run. Currently accessed via the file API after execution. Future: automatic artifact detection and tagging.

### Audit Log
Every security-relevant event (session creation/deletion, file access, command execution) is persisted as an immutable audit log entry.

### Policy Hooks
An interface (`internal/policy`) for future integration with policy engines (OPA, Rego, custom rules). The MVP uses an `AllowAll` no-op.

## Data Flow — Command Execution

```
Client
  │
  │  POST /api/v1/sessions/{id}/run
  │  { "command": "python", "args": ["script.py"] }
  │
  ▼
API Handler
  │  1. Validate session exists and is running
  │  2. Validate command (no shell metacharacters)
  │  3. Create run record (status=pending)
  │  4. Emit audit: command.started
  │
  ▼
Runner.Execute()
  │
  ▼
Docker.Exec()
  │  ContainerExecCreate — no shell, args as separate fields
  │  ContainerExecAttach — stream stdout/stderr with timeout
  │  ContainerExecInspect — get exit code
  │
  ▼
Result
  │  5. Update run record (status, exit_code, stdout, stderr, duration)
  │  6. Emit audit: command.finished / command.failed
  │
  ▼
Response
  { "run_id": "...", "status": "completed", "exit_code": 0, "stdout": "..." }
```

## Component Map

| Component | Package | Responsibility |
|---|---|---|
| API Server | `cmd/api` | HTTP handlers, routing, request parsing |
| Auth Middleware | `internal/auth` | API key hashing, validation |
| Audit Logger | `internal/audit` | Structured event persistence |
| Docker Client | `internal/docker` | Container lifecycle, secure exec |
| Workspace Manager | `internal/workspace` | File I/O with path traversal prevention |
| Runner | `internal/runner` | Orchestrates exec + persistence |
| Policy Hook | `internal/policy` | Pluggable policy interface (noop MVP) |
| DB Layer | `internal/db` | Postgres queries via sqlx |
| Config | `internal/config` | Environment-based configuration |

## Future Extension Points

| Feature | Extension Point |
|---|---|
| Kubernetes runners | Replace `internal/docker` with a Runner interface |
| Firecracker VMs | Alternative Runner implementation |
| Open Policy Agent | Replace `internal/policy.AllowAll` |
| Secrets broker | New `internal/secrets` package |
| Multi-tenancy | Add `org_id` column to sessions/runs/files |
| Persistent snapshots | Workspace versioning in `internal/workspace` |
| Network allowlists | Docker network policy in `internal/docker` |
| Enterprise audit export | Audit log streaming adapter |
