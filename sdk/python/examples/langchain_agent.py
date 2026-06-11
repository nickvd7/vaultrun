"""LangChain agent example using VaultRun as a secure code execution sandbox.

This example shows how to integrate VaultRun with LangChain to give an LLM
a safe, isolated Python environment for running arbitrary code.

Requirements:
    pip install langchain langchain-openai vaultrun-sdk

Usage:
    VAULTRUN_API_URL=http://localhost:8080 VAULTRUN_API_KEY=... \\
    OPENAI_API_KEY=... python langchain_agent.py
"""
from __future__ import annotations

import os
from typing import Any

from langchain.agents import AgentExecutor, create_tool_calling_agent
from langchain.prompts import ChatPromptTemplate
from langchain.tools import tool
from langchain_openai import ChatOpenAI

from sandbox_sdk import Client

# ── VaultRun client ────────────────────────────────────────────────────────────
vr = Client(
    os.environ["VAULTRUN_API_URL"],
    api_key=os.environ["VAULTRUN_API_KEY"],
)

# Create a shared session for the agent's lifetime.
_session = vr.create_session(image="python:3.12-slim", timeout_seconds=600)
print(f"VaultRun session: {_session.id}")


# ── LangChain tools ────────────────────────────────────────────────────────────
@tool
def run_python(code: str) -> str:
    """Execute Python code in an isolated VaultRun sandbox.

    The sandbox persists for the agent's lifetime — variables, files, and
    installed packages carry over between calls.

    Args:
        code: Python source code to execute.

    Returns:
        Combined stdout + stderr from the execution.
    """
    # Write code to workspace as a temp file, then execute it.
    vr.upload_file(_session.id, "agent_script.py", code.encode())
    result = vr.run(
        _session.id,
        command="python",
        args=["agent_script.py"],
        timeout_seconds=60,
    )
    output = ""
    if result.stdout:
        output += result.stdout
    if result.stderr:
        output += "\nSTDERR:\n" + result.stderr
    return output or "(no output)"


@tool
def install_package(package: str) -> str:
    """Install a Python package with pip inside the sandbox.

    Args:
        package: Package name (e.g. "pandas", "requests==2.31.0").

    Returns:
        pip install output.
    """
    result = vr.run(
        _session.id,
        command="pip",
        args=["install", "--quiet", package],
        timeout_seconds=120,
    )
    return result.stdout or result.stderr or "installed"


@tool
def save_artifact(filename: str, description: str = "") -> str:
    """Promote a file from the sandbox workspace to the shared artifact store.

    Call this when you've produced an output file that should be saved for the
    user (e.g. a CSV, PNG chart, or JSON report).

    Args:
        filename: Path of the file inside the sandbox workspace.
        description: Human-readable description (used as artifact name).

    Returns:
        Artifact ID and size.
    """
    name = description or filename
    artifact = vr.promote_artifact(_session.id, filename, name=name)
    return f"Artifact saved: id={artifact.id}, size={artifact.size_bytes} bytes"


# ── Agent setup ────────────────────────────────────────────────────────────────
llm = ChatOpenAI(model="gpt-4o-mini", temperature=0)

prompt = ChatPromptTemplate.from_messages([
    ("system",
     "You are a helpful data-science assistant. "
     "Use run_python to execute code, install_package to add libraries, "
     "and save_artifact to persist important output files."),
    ("human", "{input}"),
    ("placeholder", "{agent_scratchpad}"),
])

agent = create_tool_calling_agent(llm, [run_python, install_package, save_artifact], prompt)
executor = AgentExecutor(agent=agent, tools=[run_python, install_package, save_artifact], verbose=True)

if __name__ == "__main__":
    import sys
    query = " ".join(sys.argv[1:]) or (
        "Compute the first 20 Fibonacci numbers, save them to fibonacci.csv, then save it as an artifact."
    )
    try:
        result = executor.invoke({"input": query})
        print("\n=== Result ===")
        print(result["output"])
    finally:
        vr.delete_session(_session.id)
        print(f"Session {_session.id} deleted.")
