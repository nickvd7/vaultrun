"""VaultRun API client."""

from __future__ import annotations

import hashlib
import hmac
import io
import json
import os
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import IO, Any, Generator, Iterator, Optional

import requests


class VaultRunError(Exception):
    """Raised when the VaultRun API returns an error response."""

    def __init__(self, status_code: int, message: str) -> None:
        self.status_code = status_code
        super().__init__(f"VaultRun API error {status_code}: {message}")


@dataclass
class Session:
    id: str
    image: str
    status: str
    network_enabled: bool
    cpu_limit: float
    memory_limit_mb: int
    timeout_seconds: int
    created_at: str
    labels: dict[str, str] = field(default_factory=dict)
    name: Optional[str] = None
    container_id: Optional[str] = None
    stopped_at: Optional[str] = None


@dataclass
class Run:
    id: str
    session_id: str
    command: str
    args: list[str]
    status: str
    timeout_seconds: int
    created_at: str
    exit_code: Optional[int] = None
    stdout: Optional[str] = None
    stderr: Optional[str] = None
    duration_ms: Optional[int] = None
    output_truncated: bool = False
    started_at: Optional[str] = None
    finished_at: Optional[str] = None


@dataclass
class AsyncRunResult:
    """Returned by run_async — contains the pending run's ID."""
    run_id: str
    status: str
    message: str


@dataclass
class File:
    id: str
    session_id: str
    path: str
    size_bytes: int
    content_type: str
    created_at: str


@dataclass
class AuditLog:
    """A single audit trail entry."""
    id: str
    actor: str
    action: str
    timestamp: str
    session_id: Optional[str] = None
    run_id: Optional[str] = None
    metadata: Optional[dict[str, Any]] = None


@dataclass
class StreamResult:
    """Returned by :meth:`Client.stream` when the SSE stream closes."""
    run_id: str
    status: str
    exit_code: Optional[int] = None
    duration_ms: Optional[int] = None


@dataclass
class APIKey:
    id: str
    name: str
    prefix: str
    active: bool
    created_at: str
    last_used_at: Optional[str] = None
    expires_at: Optional[str] = None
    org_id: Optional[str] = None


@dataclass
class CreatedKey(APIKey):
    key: str = ""


@dataclass
class Organization:
    """A VaultRun team / org."""
    id: str
    name: str
    slug: str
    created_at: str
    updated_at: str


@dataclass
class OrgMember:
    """An actor's membership in an org with a RBAC role."""
    org_id: str
    principal: str
    role: str  # "viewer" | "executor" | "admin"
    created_at: str


@dataclass
class Snapshot:
    """A compressed workspace archive."""
    id: str
    session_id: str
    name: str
    created_by: str
    size_bytes: int
    created_at: str


@dataclass
class SharedArtifact:
    """A file promoted to the shared artifact registry."""
    id: str
    name: str
    size_bytes: int
    content_type: str
    created_by: str
    created_at: str
    session_id: Optional[str] = None


class Client:
    """VaultRun API client.

    Example::

        from sandbox_sdk import Client

        client = Client("http://localhost:8080", api_key="vr_...")
        session = client.create_session(image="python:3.12-slim")
        client.upload_file(session.id, "script.py", open("script.py", "rb"))
        result = client.run(session.id, command="python", args=["script.py"])
        print(result.stdout)
        client.delete_session(session.id)
    """

    def __init__(
        self,
        base_url: str,
        api_key: str = "",
        timeout: int = 120,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key or os.environ.get("VAULTRUN_API_KEY", "")
        self._session = requests.Session()
        self._session.headers["X-API-Key"] = self.api_key
        self._timeout = timeout

    # --- Sessions ---

    def create_session(
        self,
        *,
        name: str = "",
        image: str = "python:3.12-slim",
        network_enabled: bool = False,
        cpu_limit: float = 1.0,
        memory_limit_mb: int = 512,
        timeout_seconds: int = 300,
        labels: dict[str, str] | None = None,
    ) -> Session:
        """Create a new sandbox session."""
        body: dict[str, Any] = {
            "image": image,
            "network_enabled": network_enabled,
            "cpu_limit": cpu_limit,
            "memory_limit_mb": memory_limit_mb,
            "timeout_seconds": timeout_seconds,
        }
        if name:
            body["name"] = name
        if labels:
            body["labels"] = labels

        data = self._post("/api/v1/sessions", body)
        return self._parse_session(data)

    def get_session(self, session_id: str) -> Session:
        """Get a session by ID."""
        return self._parse_session(self._get(f"/api/v1/sessions/{session_id}"))

    def list_sessions(
        self,
        *,
        page: int = 1,
        limit: int = 20,
        label: str = "",
    ) -> list[Session]:
        """List sessions, newest first. Use page/limit to paginate.

        Pass label="key:value" to filter by a specific label.
        """
        url = f"/api/v1/sessions?page={page}&limit={limit}"
        if label:
            url += f"&label={label}"
        data = self._get(url)
        return [self._parse_session(s) for s in data.get("sessions", [])]

    def delete_session(self, session_id: str) -> None:
        """Delete a session and its container."""
        self._delete(f"/api/v1/sessions/{session_id}")

    def update_labels(
        self,
        session_id: str,
        labels: dict[str, str],
    ) -> dict[str, str]:
        """Replace the label set on a session. Pass {} to clear all labels."""
        data = self._patch(
            f"/api/v1/sessions/{session_id}/labels",
            {"labels": labels},
        )
        return data.get("labels", {})

    # --- Command execution ---

    def run(
        self,
        session_id: str,
        *,
        command: str,
        args: list[str] | None = None,
        env: dict[str, str] | None = None,
        working_dir: str = "",
        timeout_seconds: int = 30,
    ) -> Run:
        """Execute a command inside a session and return the result (blocking)."""
        body: dict[str, Any] = {
            "command": command,
            "args": args or [],
            "timeout_seconds": timeout_seconds,
        }
        if env:
            body["env"] = env
        if working_dir:
            body["working_dir"] = working_dir

        data = self._post(f"/api/v1/sessions/{session_id}/run", body)
        return self._parse_run(data)

    def run_async(
        self,
        session_id: str,
        *,
        command: str,
        args: list[str] | None = None,
        env: dict[str, str] | None = None,
        working_dir: str = "",
        timeout_seconds: int = 30,
        callback_url: str = "",
    ) -> AsyncRunResult:
        """Submit a command for non-blocking (async) execution.

        Returns immediately with the pending run's ID. Poll get_run() to check
        for completion, or supply a callback_url to receive a webhook when done.
        """
        body: dict[str, Any] = {
            "command": command,
            "args": args or [],
            "timeout_seconds": timeout_seconds,
        }
        if env:
            body["env"] = env
        if working_dir:
            body["working_dir"] = working_dir
        if callback_url:
            body["callback_url"] = callback_url

        data = self._post(f"/api/v1/sessions/{session_id}/run/async", body)
        return AsyncRunResult(
            run_id=data.get("run_id", ""),
            status=data.get("status", "pending"),
            message=data.get("message", ""),
        )

    def get_run(self, run_id: str) -> Run:
        """Get a run by ID."""
        return self._parse_run(self._get(f"/api/v1/runs/{run_id}"))

    def list_runs(self, session_id: str) -> list[Run]:
        """List all runs for a session."""
        data = self._get(f"/api/v1/sessions/{session_id}/runs")
        return [self._parse_run(r) for r in data.get("runs", [])]

    # --- Files ---

    def upload_file(
        self,
        session_id: str,
        remote_path: str,
        content: IO[bytes] | bytes | str | Path,
    ) -> File:
        """Upload a file to a session workspace."""
        if isinstance(content, (str, Path)):
            content = open(content, "rb")
        if isinstance(content, bytes):
            content = io.BytesIO(content)

        files = {"file": (Path(remote_path).name, content)}
        data_fields = {"path": remote_path}

        resp = self._session.post(
            self.base_url + f"/api/v1/sessions/{session_id}/files",
            files=files,
            data=data_fields,
            timeout=self._timeout,
        )
        self._raise_for_status(resp)
        return self._parse_file(resp.json())

    def download_file(self, session_id: str, remote_path: str) -> bytes:
        """Download a single file from a session workspace."""
        clean = remote_path.lstrip("/")
        resp = self._session.get(
            self.base_url + f"/api/v1/sessions/{session_id}/files/{clean}",
            timeout=self._timeout,
        )
        self._raise_for_status(resp)
        return resp.content

    def download_workspace(self, session_id: str) -> bytes:
        """Download the entire session workspace as a ZIP archive."""
        resp = self._session.get(
            self.base_url + f"/api/v1/sessions/{session_id}/workspace.zip",
            timeout=0,  # no timeout — workspace may be large
        )
        self._raise_for_status(resp)
        return resp.content

    def list_files(self, session_id: str) -> list[File]:
        """List files in a session workspace."""
        data = self._get(f"/api/v1/sessions/{session_id}/files")
        return [self._parse_file(f) for f in data.get("files", [])]

    # --- API key management ---

    def list_keys(self) -> list[APIKey]:
        """List all API keys (does not reveal plaintext keys)."""
        data = self._get("/api/v1/keys")
        return [self._parse_api_key(k) for k in data.get("api_keys", [])]

    def create_key(
        self,
        name: str,
        *,
        expires_at: Optional[datetime] = None,
    ) -> CreatedKey:
        """Create a new API key. The plaintext key is only available in the returned object."""
        body: dict[str, Any] = {"name": name}
        if expires_at is not None:
            if expires_at.tzinfo is None:
                expires_at = expires_at.replace(tzinfo=timezone.utc)
            body["expires_at"] = expires_at.isoformat()
        data = self._post("/api/v1/keys", body)
        return self._parse_created_key(data)

    def revoke_key(self, key_id: str) -> None:
        """Revoke an API key by ID."""
        self._delete(f"/api/v1/keys/{key_id}")

    # --- Audit ---

    def list_audit_logs(
        self,
        session_id: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> list["AuditLog"]:
        """Return audit log entries for the current actor.

        Master key holders see all actors' entries; regular keys see only their
        own sessions' entries. Entries are ordered newest-first.

        Args:
            session_id: restrict results to a single session (optional).
            limit: maximum entries to return (default 50).
            offset: pagination offset.

        Returns:
            List of :class:`AuditLog` entries.
        """
        params: list[str] = []
        if session_id:
            params.append(f"session_id={session_id}")
        if limit != 50:
            params.append(f"limit={limit}")
        if offset:
            params.append(f"offset={offset}")
        path = "/api/v1/audit"
        if params:
            path += "?" + "&".join(params)
        data = self._get(path)
        return [self._parse_audit_log(e) for e in data.get("audit_logs", [])]

    # --- Streaming ---

    def stream(
        self,
        session_id: str,
        command: str,
        args: Optional[list[str]] = None,
        env: Optional[dict[str, str]] = None,
        working_dir: Optional[str] = None,
        timeout_seconds: int = 30,
        stdout: Optional[IO[str]] = None,
        stderr: Optional[IO[str]] = None,
    ) -> "StreamResult":
        """Execute a command via SSE streaming, writing output chunks as they arrive.

        Unlike :meth:`run`, which buffers all output and returns when the command
        finishes, ``stream`` writes stdout/stderr to the provided file-like objects
        as each chunk arrives.  This is useful for long-running commands where you
        want to observe progress in real time.

        Args:
            session_id: target session.
            command: executable name (no shell — no metacharacters).
            args: positional arguments.
            env: extra environment variables injected into the exec.
            working_dir: working directory inside the container.
            timeout_seconds: server-side execution timeout.
            stdout: writable text stream for stdout chunks (default: discard).
            stderr: writable text stream for stderr chunks (default: discard).

        Returns:
            :class:`StreamResult` with the final status, exit code, and duration.

        Example::

            import sys
            result = client.stream(
                session.id, "python", args=["script.py"],
                stdout=sys.stdout, stderr=sys.stderr,
            )
            print(f"exit code: {result.exit_code}")
        """
        body: dict[str, Any] = {
            "command": command,
            "args": args or [],
            "timeout_seconds": timeout_seconds,
        }
        if env:
            body["env"] = env
        if working_dir:
            body["working_dir"] = working_dir

        resp = self._session.post(
            self.base_url + f"/api/v1/sessions/{session_id}/run/stream",
            json=body,
            headers={"Accept": "text/event-stream"},
            stream=True,
            timeout=None,  # caller controls duration via context / timeout_seconds
        )
        self._raise_for_status(resp)

        final: dict[str, Any] = {}
        for line in resp.iter_lines():
            if not line:
                continue
            if isinstance(line, bytes):
                line = line.decode()
            if not line.startswith("data:"):
                continue
            payload = line[5:].strip()
            if not payload:
                continue
            try:
                event = json.loads(payload)
            except ValueError:
                continue
            etype = event.get("type", "")
            if etype == "stdout" and stdout is not None:
                stdout.write(event.get("data", ""))
            elif etype == "stderr" and stderr is not None:
                stderr.write(event.get("data", ""))
            elif etype == "done":
                final = event
                break

        return StreamResult(
            run_id=final.get("run_id", ""),
            status=final.get("status", "unknown"),
            exit_code=final.get("exit_code"),
            duration_ms=final.get("duration_ms"),
        )

    # --- Organizations & RBAC ---

    def create_org(self, name: str, slug: Optional[str] = None) -> Organization:
        """Create a new organization. Requires master key.

        Args:
            name: human-readable org name.
            slug: URL-safe identifier; auto-generated from name if omitted.

        Returns:
            :class:`Organization`
        """
        body: dict[str, Any] = {"name": name}
        if slug:
            body["slug"] = slug
        d = self._post("/api/v1/orgs", body)
        return self._parse_org(d)

    def list_orgs(self) -> list[Organization]:
        """List all organizations. Requires master key."""
        d = self._get("/api/v1/orgs")
        return [self._parse_org(o) for o in d.get("orgs", [])]

    def get_org(self, org_id: str) -> Organization:
        """Fetch a single org by ID. Accessible to org members."""
        d = self._get(f"/api/v1/orgs/{org_id}")
        return self._parse_org(d)

    def delete_org(self, org_id: str) -> None:
        """Delete an org and all its members. Requires master key."""
        self._delete(f"/api/v1/orgs/{org_id}")

    def add_org_member(
        self,
        org_id: str,
        principal: str,
        role: str = "executor",
    ) -> OrgMember:
        """Add or update an org member.

        Args:
            org_id: target organization ID.
            principal: API key name (the actor string).
            role: one of ``"viewer"``, ``"executor"``, or ``"admin"``.

        Requires master key or org admin role.
        """
        body: dict[str, Any] = {"principal": principal, "role": role}
        d = self._post(f"/api/v1/orgs/{org_id}/members", body)
        return self._parse_org_member(d)

    def list_org_members(self, org_id: str) -> list[OrgMember]:
        """List all members of an org. Accessible to org members."""
        d = self._get(f"/api/v1/orgs/{org_id}/members")
        return [self._parse_org_member(m) for m in d.get("members", [])]

    def remove_org_member(self, org_id: str, principal: str) -> None:
        """Remove a principal from an org. Requires master key or org admin."""
        self._delete(f"/api/v1/orgs/{org_id}/members/{principal}")

    def list_org_sessions(self, org_id: str) -> list[Session]:
        """List active sessions that belong to the org.

        The caller must be an org member; the server filters by role.
        """
        d = self._get(f"/api/v1/orgs/{org_id}/sessions")
        return [self._parse_session(s) for s in d.get("sessions", [])]

    # --- Snapshots ---

    def create_snapshot(self, session_id: str, *, name: str) -> Snapshot:
        """Create a snapshot archive of a session's workspace."""
        data = self._post(f"/api/v1/sessions/{session_id}/snapshots", {"name": name})
        return Snapshot(**{k: v for k, v in data.items() if k in Snapshot.__dataclass_fields__})

    def list_snapshots(self, session_id: str) -> list[Snapshot]:
        """List all snapshots for a session."""
        data = self._get(f"/api/v1/sessions/{session_id}/snapshots")
        return [Snapshot(**{k: v for k, v in s.items() if k in Snapshot.__dataclass_fields__})
                for s in data.get("snapshots", [])]

    def download_snapshot(self, snapshot_id: str) -> bytes:
        """Download a snapshot archive as bytes."""
        resp = self._session.get(
            f"{self.base_url}/api/v1/snapshots/{snapshot_id}/download",
            timeout=self._timeout,
        )
        self._raise_for_status(resp)
        return resp.content

    def delete_snapshot(self, snapshot_id: str) -> None:
        """Delete a snapshot."""
        self._delete(f"/api/v1/snapshots/{snapshot_id}")

    # --- Artifacts ---

    def promote_artifact(
        self,
        session_id: str,
        path: str,
        *,
        name: str = "",
    ) -> SharedArtifact:
        """Promote a workspace file to the shared artifact registry."""
        body: dict[str, Any] = {"path": path}
        if name:
            body["name"] = name
        data = self._post(f"/api/v1/sessions/{session_id}/artifacts", body)
        return SharedArtifact(**{k: v for k, v in data.items() if k in SharedArtifact.__dataclass_fields__})

    def list_artifacts(self) -> list[SharedArtifact]:
        """List shared artifacts visible to the caller."""
        data = self._get("/api/v1/artifacts")
        return [SharedArtifact(**{k: v for k, v in a.items() if k in SharedArtifact.__dataclass_fields__})
                for a in data.get("artifacts", [])]

    def download_artifact(self, artifact_id: str) -> bytes:
        """Download a shared artifact as bytes."""
        resp = self._session.get(
            f"{self.base_url}/api/v1/artifacts/{artifact_id}/download",
            timeout=self._timeout,
        )
        self._raise_for_status(resp)
        return resp.content

    def delete_artifact(self, artifact_id: str) -> None:
        """Delete a shared artifact."""
        self._delete(f"/api/v1/artifacts/{artifact_id}")

    # --- Webhook signature verification ---

    @staticmethod
    def verify_webhook_signature(
        payload: bytes,
        signature_header: str,
        secret: str,
    ) -> bool:
        """Verify the X-VaultRun-Signature header on a callback POST.

        Args:
            payload: raw request body bytes
            signature_header: value of the X-VaultRun-Signature header
            secret: the WEBHOOK_SECRET configured on the server

        Returns True when the signature is valid, False otherwise.

        Example (Flask)::

            @app.route("/webhook", methods=["POST"])
            def webhook():
                if not Client.verify_webhook_signature(
                    request.data,
                    request.headers.get("X-VaultRun-Signature", ""),
                    os.environ["WEBHOOK_SECRET"],
                ):
                    abort(401)
                ...
        """
        if not signature_header.startswith("sha256="):
            return False
        expected = signature_header[7:]
        mac = hmac.new(secret.encode(), payload, hashlib.sha256)
        computed = mac.hexdigest()
        return hmac.compare_digest(computed, expected)

    # --- Internal helpers ---

    def _get(self, path: str) -> dict:
        resp = self._session.get(self.base_url + path, timeout=self._timeout)
        self._raise_for_status(resp)
        return resp.json()

    def _post(self, path: str, body: dict) -> dict:
        resp = self._session.post(self.base_url + path, json=body, timeout=self._timeout)
        self._raise_for_status(resp)
        return resp.json()

    def _patch(self, path: str, body: dict) -> dict:
        resp = self._session.patch(self.base_url + path, json=body, timeout=self._timeout)
        self._raise_for_status(resp)
        return resp.json()

    def _delete(self, path: str) -> None:
        resp = self._session.delete(self.base_url + path, timeout=self._timeout)
        self._raise_for_status(resp)

    @staticmethod
    def _raise_for_status(resp: requests.Response) -> None:
        if resp.status_code >= 400:
            try:
                msg = resp.json().get("error", resp.text)
            except Exception:
                msg = resp.text
            raise VaultRunError(resp.status_code, msg)

    @staticmethod
    def _parse_api_key(d: dict) -> APIKey:
        return APIKey(
            id=d["id"],
            name=d["name"],
            prefix=d["prefix"],
            active=d["active"],
            created_at=d["created_at"],
            last_used_at=d.get("last_used_at"),
            expires_at=d.get("expires_at"),
        )

    @staticmethod
    def _parse_created_key(d: dict) -> CreatedKey:
        return CreatedKey(
            id=d["id"],
            name=d["name"],
            prefix=d["prefix"],
            active=d["active"],
            created_at=d["created_at"],
            last_used_at=d.get("last_used_at"),
            expires_at=d.get("expires_at"),
            key=d["key"],
        )

    @staticmethod
    def _parse_session(d: dict) -> Session:
        raw_labels = d.get("labels") or {}
        labels = {str(k): str(v) for k, v in raw_labels.items()}
        return Session(
            id=d["id"],
            name=d.get("name"),
            image=d["image"],
            status=d["status"],
            container_id=d.get("container_id"),
            network_enabled=d["network_enabled"],
            cpu_limit=d["cpu_limit"],
            memory_limit_mb=d["memory_limit_mb"],
            timeout_seconds=d["timeout_seconds"],
            labels=labels,
            created_at=d["created_at"],
            stopped_at=d.get("stopped_at"),
        )

    @staticmethod
    def _parse_run(d: dict) -> Run:
        return Run(
            id=d["id"],
            session_id=d["session_id"],
            command=d["command"],
            args=d.get("args", []),
            status=d["status"],
            exit_code=d.get("exit_code"),
            stdout=d.get("stdout"),
            stderr=d.get("stderr"),
            duration_ms=d.get("duration_ms"),
            output_truncated=d.get("output_truncated", False),
            timeout_seconds=d["timeout_seconds"],
            created_at=d["created_at"],
            started_at=d.get("started_at"),
            finished_at=d.get("finished_at"),
        )

    @staticmethod
    def _parse_file(d: dict) -> File:
        return File(
            id=d["id"],
            session_id=d["session_id"],
            path=d["path"],
            size_bytes=d["size_bytes"],
            content_type=d["content_type"],
            created_at=d["created_at"],
        )

    @staticmethod
    def _parse_audit_log(d: dict) -> AuditLog:
        return AuditLog(
            id=d["id"],
            actor=d["actor"],
            action=d["action"],
            timestamp=d["timestamp"],
            session_id=d.get("session_id"),
            run_id=d.get("run_id"),
            metadata=d.get("metadata"),
        )

    @staticmethod
    def _parse_org(d: dict) -> Organization:
        return Organization(
            id=d["id"],
            name=d["name"],
            slug=d["slug"],
            created_at=d["created_at"],
            updated_at=d["updated_at"],
        )

    @staticmethod
    def _parse_org_member(d: dict) -> OrgMember:
        return OrgMember(
            org_id=d["org_id"],
            principal=d["principal"],
            role=d["role"],
            created_at=d["created_at"],
        )
