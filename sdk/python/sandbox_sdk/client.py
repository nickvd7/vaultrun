"""VaultRun API client."""

from __future__ import annotations

import os
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import IO, Any, Optional

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
    started_at: Optional[str] = None
    finished_at: Optional[str] = None


@dataclass
class File:
    id: str
    session_id: str
    path: str
    size_bytes: int
    content_type: str
    created_at: str


@dataclass
class APIKey:
    id: str
    name: str
    prefix: str
    active: bool
    created_at: str
    last_used_at: Optional[str] = None
    expires_at: Optional[str] = None


@dataclass
class CreatedKey(APIKey):
    key: str = ""


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

        data = self._post("/api/v1/sessions", body)
        return self._parse_session(data)

    def get_session(self, session_id: str) -> Session:
        """Get a session by ID."""
        return self._parse_session(self._get(f"/api/v1/sessions/{session_id}"))

    def list_sessions(self, *, page: int = 1, limit: int = 20) -> list[Session]:
        """List sessions, newest first. Use page/limit to paginate."""
        data = self._get(f"/api/v1/sessions?page={page}&limit={limit}")
        return [self._parse_session(s) for s in data.get("sessions", [])]

    def delete_session(self, session_id: str) -> None:
        """Delete a session and its container."""
        self._delete(f"/api/v1/sessions/{session_id}")

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
        """Execute a command inside a session and return the result."""
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
            import io
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
        """Download a file from a session workspace."""
        resp = self._session.get(
            self.base_url + f"/api/v1/sessions/{session_id}/files/{remote_path}",
            timeout=self._timeout,
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
        """Create a new API key.  The plaintext key is only available in the returned object."""
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

    # --- Internal helpers ---

    def _get(self, path: str) -> dict:
        resp = self._session.get(self.base_url + path, timeout=self._timeout)
        self._raise_for_status(resp)
        return resp.json()

    def _post(self, path: str, body: dict) -> dict:
        resp = self._session.post(self.base_url + path, json=body, timeout=self._timeout)
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
