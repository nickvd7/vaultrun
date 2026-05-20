"""Tests for the VaultRun Python SDK client."""

from __future__ import annotations

import responses as rsps

import pytest

from sandbox_sdk.client import Client, File, Run, Session, VaultRunError

BASE_URL = "http://testserver"

# ---------------------------------------------------------------------------
# Shared fixtures & helpers
# ---------------------------------------------------------------------------

SESSION_ID = "sess-abc123"
RUN_ID = "run-xyz789"
FILE_ID = "file-def456"
CREATED_AT = "2024-01-15T12:00:00Z"

SESSION_JSON = {
    "id": SESSION_ID,
    "name": "my-session",
    "image": "python:3.12-slim",
    "status": "running",
    "container_id": "cnt-001",
    "network_enabled": False,
    "cpu_limit": 1.0,
    "memory_limit_mb": 512,
    "timeout_seconds": 300,
    "created_at": CREATED_AT,
    "stopped_at": None,
}

RUN_JSON = {
    "id": RUN_ID,
    "session_id": SESSION_ID,
    "command": "python",
    "args": ["script.py"],
    "status": "success",
    "exit_code": 0,
    "stdout": "hello world\n",
    "stderr": "",
    "duration_ms": 250,
    "timeout_seconds": 30,
    "created_at": CREATED_AT,
    "started_at": CREATED_AT,
    "finished_at": CREATED_AT,
}

FILE_JSON = {
    "id": FILE_ID,
    "session_id": SESSION_ID,
    "path": "/workspace/script.py",
    "size_bytes": 128,
    "content_type": "text/x-python",
    "created_at": CREATED_AT,
}


@pytest.fixture
def client() -> Client:
    return Client(BASE_URL, api_key="vr_test_key")


# ---------------------------------------------------------------------------
# test_create_session_success
# ---------------------------------------------------------------------------

@rsps.activate
def test_create_session_success(client: Client) -> None:
    rsps.add(
        rsps.POST,
        f"{BASE_URL}/api/v1/sessions",
        json=SESSION_JSON,
        status=201,
    )

    session = client.create_session(
        name="my-session",
        image="python:3.12-slim",
        network_enabled=False,
        cpu_limit=1.0,
        memory_limit_mb=512,
        timeout_seconds=300,
    )

    assert isinstance(session, Session)
    assert session.id == SESSION_ID
    assert session.name == "my-session"
    assert session.image == "python:3.12-slim"
    assert session.status == "running"
    assert session.network_enabled is False
    assert session.cpu_limit == 1.0
    assert session.memory_limit_mb == 512
    assert session.timeout_seconds == 300
    assert session.created_at == CREATED_AT
    assert session.container_id == "cnt-001"
    assert session.stopped_at is None


# ---------------------------------------------------------------------------
# test_create_session_api_error
# ---------------------------------------------------------------------------

@rsps.activate
def test_create_session_api_error(client: Client) -> None:
    rsps.add(
        rsps.POST,
        f"{BASE_URL}/api/v1/sessions",
        json={"error": "bad"},
        status=422,
    )

    with pytest.raises(VaultRunError) as exc_info:
        client.create_session(image="python:3.12-slim")

    err = exc_info.value
    assert err.status_code == 422
    assert "bad" in str(err)


# ---------------------------------------------------------------------------
# test_run_success
# ---------------------------------------------------------------------------

@rsps.activate
def test_run_success(client: Client) -> None:
    rsps.add(
        rsps.POST,
        f"{BASE_URL}/api/v1/sessions/{SESSION_ID}/run",
        json=RUN_JSON,
        status=200,
    )

    run = client.run(SESSION_ID, command="python", args=["script.py"])

    assert isinstance(run, Run)
    assert run.id == RUN_ID
    assert run.session_id == SESSION_ID
    assert run.command == "python"
    assert run.args == ["script.py"]
    assert run.status == "success"
    assert run.exit_code == 0
    assert run.stdout == "hello world\n"
    assert run.stderr == ""
    assert run.duration_ms == 250
    assert run.timeout_seconds == 30


# ---------------------------------------------------------------------------
# test_run_timeout_status
# ---------------------------------------------------------------------------

@rsps.activate
def test_run_timeout_status(client: Client) -> None:
    timeout_run_json = dict(RUN_JSON, status="timeout", exit_code=None, stdout=None, stderr=None)
    rsps.add(
        rsps.POST,
        f"{BASE_URL}/api/v1/sessions/{SESSION_ID}/run",
        json=timeout_run_json,
        status=200,
    )

    run = client.run(SESSION_ID, command="sleep", args=["999"])

    assert run.status == "timeout"
    assert run.exit_code is None


# ---------------------------------------------------------------------------
# test_list_sessions
# ---------------------------------------------------------------------------

@rsps.activate
def test_list_sessions(client: Client) -> None:
    second_session = dict(SESSION_JSON, id="sess-second", name="second")
    rsps.add(
        rsps.GET,
        f"{BASE_URL}/api/v1/sessions",
        json={"sessions": [SESSION_JSON, second_session]},
        status=200,
    )

    sessions = client.list_sessions()

    assert len(sessions) == 2
    assert all(isinstance(s, Session) for s in sessions)
    assert sessions[0].id == SESSION_ID
    assert sessions[1].id == "sess-second"


# ---------------------------------------------------------------------------
# test_list_runs
# ---------------------------------------------------------------------------

@rsps.activate
def test_list_runs(client: Client) -> None:
    second_run = dict(RUN_JSON, id="run-second")
    third_run = dict(RUN_JSON, id="run-third")
    rsps.add(
        rsps.GET,
        f"{BASE_URL}/api/v1/sessions/{SESSION_ID}/runs",
        json={"runs": [RUN_JSON, second_run, third_run]},
        status=200,
    )

    runs = client.list_runs(SESSION_ID)

    assert len(runs) == 3
    assert all(isinstance(r, Run) for r in runs)
    assert runs[0].id == RUN_ID
    assert runs[1].id == "run-second"
    assert runs[2].id == "run-third"


# ---------------------------------------------------------------------------
# test_upload_file_success
# ---------------------------------------------------------------------------

@rsps.activate
def test_upload_file_success(client: Client) -> None:
    rsps.add(
        rsps.POST,
        f"{BASE_URL}/api/v1/sessions/{SESSION_ID}/files",
        json=FILE_JSON,
        status=201,
    )

    content = b"print('hello')\n"
    uploaded = client.upload_file(SESSION_ID, "/workspace/script.py", content)

    assert isinstance(uploaded, File)
    assert uploaded.id == FILE_ID
    assert uploaded.session_id == SESSION_ID
    assert uploaded.path == "/workspace/script.py"
    assert uploaded.size_bytes == 128
    assert uploaded.content_type == "text/x-python"
    assert uploaded.created_at == CREATED_AT


# ---------------------------------------------------------------------------
# test_download_file_success
# ---------------------------------------------------------------------------

@rsps.activate
def test_download_file_success(client: Client) -> None:
    file_bytes = b"print('hello')\n"
    rsps.add(
        rsps.GET,
        f"{BASE_URL}/api/v1/sessions/{SESSION_ID}/files//workspace/script.py",
        body=file_bytes,
        status=200,
        content_type="application/octet-stream",
    )

    result = client.download_file(SESSION_ID, "/workspace/script.py")

    assert result == file_bytes


# ---------------------------------------------------------------------------
# test_delete_session
# ---------------------------------------------------------------------------

@rsps.activate
def test_delete_session(client: Client) -> None:
    rsps.add(
        rsps.DELETE,
        f"{BASE_URL}/api/v1/sessions/{SESSION_ID}",
        status=204,
        body=b"",
    )

    # Should not raise any exception
    client.delete_session(SESSION_ID)


# ---------------------------------------------------------------------------
# test_get_run
# ---------------------------------------------------------------------------

@rsps.activate
def test_get_run(client: Client) -> None:
    rsps.add(
        rsps.GET,
        f"{BASE_URL}/api/v1/runs/{RUN_ID}",
        json=RUN_JSON,
        status=200,
    )

    run = client.get_run(RUN_ID)

    assert isinstance(run, Run)
    assert run.id == RUN_ID
    assert run.session_id == SESSION_ID
    assert run.command == "python"
    assert run.args == ["script.py"]
    assert run.status == "success"
    assert run.exit_code == 0
    assert run.stdout == "hello world\n"
    assert run.stderr == ""


# ---------------------------------------------------------------------------
# test_missing_api_key_still_sends_header
# ---------------------------------------------------------------------------

@rsps.activate
def test_missing_api_key_still_sends_header() -> None:
    """Client with empty api_key must still send the X-API-Key header (value is empty string)."""
    rsps.add(
        rsps.GET,
        f"{BASE_URL}/api/v1/sessions",
        json={"sessions": []},
        status=200,
    )

    # Create a client with an explicitly empty api_key, bypassing any env variable.
    import os
    env_backup = os.environ.pop("VAULTRUN_API_KEY", None)
    try:
        empty_key_client = Client(BASE_URL, api_key="")
        empty_key_client.list_sessions()
    finally:
        if env_backup is not None:
            os.environ["VAULTRUN_API_KEY"] = env_backup

    assert len(rsps.calls) == 1
    sent_headers = rsps.calls[0].request.headers
    assert "X-API-Key" in sent_headers
    assert sent_headers["X-API-Key"] == ""
