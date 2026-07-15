# vaultrun-sdk

Python SDK for [VaultRun](https://vaultrun.dev) — the self-hosted secure runtime for AI agents.

VaultRun lets AI agents safely execute code, query databases, call cloud APIs, and manage
files inside isolated Docker sandboxes running on your own infrastructure. This package is
the typed Python client for the VaultRun REST API.

## Install

```bash
pip install vaultrun-sdk
```

## Quickstart

```python
from sandbox_sdk import Client

client = Client("http://localhost:8080", api_key="vr_...")

session = client.create_session(image="python:3.12-slim", memory_limit_mb=256)
client.upload_file(session.id, "script.py", open("script.py", "rb"))

result = client.run(session.id, command="python", args=["script.py"])
print(result.stdout)

client.delete_session(session.id)
```

## Features

- Sessions: create, list, get, delete isolated sandbox sessions
- Runs: execute commands, stream output, fetch run details and logs
- Files: upload, list, and download workspace files
- Keys, organizations, audit logs, snapshots, artifacts, images, and session stats

## Requirements

- Python 3.10+
- A running [VaultRun](https://github.com/nickvd7/vaultrun) instance and an API key (`vr_...`)

## Links

- Website: https://vaultrun.dev
- AI index (`llms.txt`): https://vaultrun.dev/llms.txt
- Source & docs: https://github.com/nickvd7/vaultrun
- Issues: https://github.com/nickvd7/vaultrun/issues
- PyPI: https://pypi.org/project/vaultrun-sdk/

## License

Apache 2.0
