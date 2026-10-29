# Session evidence packs

Status: **shipped**.

An evidence pack is a signed record of everything one sandbox session did, made
for someone who does not run your VaultRun server and should not have to trust
it: an auditor, a regulator, a customer's security team.

The audit log's HMAC signatures prove integrity only to whoever holds
`AUDIT_HMAC_KEY`, which is you. An evidence pack is signed with an **Ed25519**
key: the verifier needs only the public key and the file, works offline, and
detects a change to any byte.

## What a pack contains

| Section | Content |
|---|---|
| `session` | Image, sandbox runtime (e.g. `runsc` for gVisor, from the signed `session.created` entry), network (off, allowlist, unrestricted), CPU/memory/timeout limits, creator, timestamps |
| `runs` | Every command, oldest first: command, arguments, working directory, status, exit code, timing, stdout/stderr (up to 64 KiB each, marked when cut) and the SHA-256 of the **full** output |
| `files` | Every workspace file at export time with size and SHA-256. Symlinks are listed, never followed |
| `audit` | The session's audit entries with the server's verification result for each (`valid`, `invalid` with the reason, ...) at export time |
| `notes` | What the pack leaves out (limits reached, workspace already removed) |

**Audit scope:** a regular API key gets its own audit entries only, the same
scope as `GET /api/v1/audit`; the master key gets all of them. The pack records
which (`audit.scope`), and the verifier says so.

Limits per pack: 10,000 runs, 10,000 files, 50,000 audit entries (newest kept;
noted in the pack when reached).

## Setup

`make up` generates `EVIDENCE_SIGNING_KEY` in `.env` when it is empty. Otherwise
set it yourself, either form:

```bash
vaultrun evidence keygen            # prints EVIDENCE_SIGNING_KEY=<64 hex>, writes vaultrun-evidence.pub
EVIDENCE_SIGNING_KEY=/etc/vaultrun/evidence.pem   # or a PKCS#8 PEM file:
                                                  # openssl genpkey -algorithm ed25519 -out evidence.pem
```

The API refuses to start with an unusable key. Without one, evidence export
answers `501`.

Hand the **public** key to whoever verifies packs:

```bash
vaultrun evidence public-key > vaultrun-evidence.pub
```

Keep the private key as secret as `AUDIT_HMAC_KEY`. Rotating it means a new
public key; packs signed before stay verifiable with the old public key, so
keep old public keys with the packs they signed (the pack names its `key_id`).

## Export

```bash
vaultrun evidence export <session-id> -o evidence.json
# or
curl -H "X-API-Key: $KEY" https://vaultrun.example.com/api/v1/sessions/<id>/evidence -o evidence.json
```

Exports work for stopped and deleted sessions too (their runs and audit entries
are kept; deleted workspaces have no files). Every export is itself audited as
`evidence.exported`.

## Verify (offline)

```bash
vaultrun evidence verify evidence.json --key vaultrun-evidence.pub
```

```
VALID signature (ed25519, key 37718810960690c1)

Pack      c79117f1-..., exported 2026-10-29 07:24:18 UTC by master (vaultrun 0.4.12)
Session   fc37619d-... "invoice-agent", running
          image python:3.12-slim, runtime runsc, 1 CPU, 512 MB
          network off
Runs      1 (1 exit 0, 0 other)
          2026-10-29 07:24:18  exit 0   python report.py
Files     1
Audit     3 entries (scope all), 3 verified by the server at export
```

A changed pack, or one signed with another key, prints `NOT VALID` and exits
with status 1. `--json` prints the verified content; `--strict` also fails on
warnings (for example audit entries the server could not verify at export).

The verifier needs no API key and no network. It is part of the `vaultrun` CLI
binary from the GitHub releases.

## Format

```json
{
  "pack": "<base64 of the pack JSON>",
  "signature": { "alg": "ed25519", "key_id": "<16 hex>", "value": "<base64>" }
}
```

The signature covers the exact pack bytes, so reformatting the file (`jq .`)
does not break it and verifiers need no JSON canonicalisation. `key_id` is the
first 8 bytes of the SHA-256 of the public key, in hex. Schema:
`vaultrun.evidence.pack.v1`. To verify without the CLI: base64-decode `pack`,
check the Ed25519 signature over those bytes, then parse them as JSON.

## What a pack proves, and what not

- It proves the pack is unchanged since the server holding the private key
  signed it, and what that server recorded about the session.
- It does not prove the server was honest when it signed. That trust sits with
  whoever controls the key, the same as for any signed log. The audit entries'
  own chain status, recorded per entry, shows whether the log was tampered with
  before export.

API: `GET /api/v1/sessions/:id/evidence`, `GET /api/v1/evidence/public-key`
(viewer access to the session; the public key needs any valid key).
