"""AutoGen multi-agent example using VaultRun as a secure code execution backend.

This example replaces AutoGen's built-in LocalCommandLineCodeExecutor with
VaultRun's isolated Docker sandbox, so code is never executed on the host.

Requirements:
    pip install pyautogen vaultrun-sdk

Usage:
    VAULTRUN_API_URL=http://localhost:8080 VAULTRUN_API_KEY=... \\
    OPENAI_API_KEY=sk-... python autogen_agent.py
"""
from __future__ import annotations

import os
import re
import textwrap

import autogen
from sandbox_sdk import Client

# ── VaultRun client ────────────────────────────────────────────────────────────
vr = Client(
    os.environ["VAULTRUN_API_URL"],
    api_key=os.environ["VAULTRUN_API_KEY"],
)
session = vr.create_session(image="python:3.12-slim", timeout_seconds=1800)
print(f"VaultRun session: {session.id}")


class VaultRunCodeExecutor:
    """AutoGen-compatible code executor that runs code in a VaultRun sandbox."""

    def __init__(self, client: Client, session_id: str) -> None:
        self._client = client
        self._session_id = session_id

    def execute_code_blocks(self, code_blocks: list) -> tuple[int, str]:
        """Execute a list of code blocks and return (exit_code, output)."""
        outputs = []
        for block in code_blocks:
            lang = getattr(block, "language", "python").lower()
            code = getattr(block, "code", "")
            if lang not in ("python", "py", "sh", "bash", "shell"):
                outputs.append(f"# Skipped block (unsupported language: {lang})")
                continue

            command = "python" if lang in ("python", "py") else "sh"
            script = "agent_code.py" if command == "python" else "agent_code.sh"
            self._client.upload_file(self._session_id, script, code.encode())
            result = self._client.run(
                self._session_id,
                command=command,
                args=[script],
                timeout_seconds=120,
            )
            out = result.stdout or ""
            err = result.stderr or ""
            combined = out + (("\nSTDERR:\n" + err) if err else "")
            outputs.append(combined)
            exit_code = result.exit_code if result.exit_code is not None else 0
            if exit_code != 0:
                return exit_code, "\n".join(outputs)
        return 0, "\n".join(outputs)


# ── AutoGen agents ─────────────────────────────────────────────────────────────
config_list = [{"model": "gpt-4o-mini", "api_key": os.environ["OPENAI_API_KEY"]}]
llm_config = {"config_list": config_list, "temperature": 0}

executor = VaultRunCodeExecutor(vr, session.id)

user_proxy = autogen.UserProxyAgent(
    name="user_proxy",
    human_input_mode="NEVER",
    max_consecutive_auto_reply=10,
    code_execution_config={
        "executor": executor,
    },
    is_termination_msg=lambda msg: "TERMINATE" in msg.get("content", ""),
)

assistant = autogen.AssistantAgent(
    name="assistant",
    llm_config=llm_config,
    system_message=(
        "You are a helpful assistant. Write Python code to solve tasks. "
        "When you're done, include 'TERMINATE' in your final message."
    ),
)

if __name__ == "__main__":
    import sys
    task = " ".join(sys.argv[1:]) or (
        "Write a Python script that prints a 10x10 multiplication table "
        "and saves it to table.txt. Show me the file contents."
    )
    try:
        user_proxy.initiate_chat(assistant, message=task)
    finally:
        vr.delete_session(session.id)
        print(f"Session {session.id} deleted.")
