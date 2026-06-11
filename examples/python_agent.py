#!/usr/bin/env python3
"""
Example: Python agent using the VaultRun SDK.

Demonstrates the full lifecycle:
  1. Create a session
  2. Upload a script
  3. Execute it
  4. Download the output
  5. Clean up
"""

import os
import sys
import textwrap

# Add the SDK to the path (for local dev)
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "sdk", "python"))

from sandbox_sdk import Client, VaultRunError

API_URL = os.environ.get("VAULTRUN_API_URL", "http://localhost:8080")
API_KEY = os.environ.get("VAULTRUN_API_KEY", "")


def main():
    client = Client(API_URL, api_key=API_KEY)

    print("1. Creating session…")
    session = client.create_session(
        name="python-demo",
        image="python:3.12-slim",
        network_enabled=False,
        cpu_limit=0.5,
        memory_limit_mb=256,
    )
    print(f"   Session ID: {session.id}  Status: {session.status}")

    try:
        # 2. Upload a Python script
        script = textwrap.dedent("""\
            import sys, json, math

            data = {"numbers": list(range(10))}
            result = {
                "squares": [x**2 for x in data["numbers"]],
                "sum": sum(data["numbers"]),
                "pi": math.pi,
            }
            print(json.dumps(result, indent=2))
            sys.exit(0)
        """)

        print("\n2. Uploading script…")
        f = client.upload_file(session.id, "compute.py", script.encode())
        print(f"   Uploaded {f.path} ({f.size_bytes} bytes)")

        # 3. Execute the script
        print("\n3. Executing script…")
        run = client.run(
            session.id,
            command="python",
            args=["compute.py"],
            timeout_seconds=10,
        )
        print(f"   Run ID:    {run.id}")
        print(f"   Status:    {run.status}")
        print(f"   Exit code: {run.exit_code}")
        print(f"   Duration:  {run.duration_ms}ms")

        if run.stdout:
            print("\n--- stdout ---")
            print(run.stdout)

        if run.stderr:
            print("\n--- stderr ---")
            print(run.stderr, file=sys.stderr)

        # 4. List files
        print("\n4. Files in workspace:")
        for file in client.list_files(session.id):
            print(f"   {file.path}  ({file.size_bytes} bytes)")

    finally:
        # 5. Clean up
        print("\n5. Deleting session…")
        client.delete_session(session.id)
        print("   Done.")


if __name__ == "__main__":
    main()
