# Operations: availability, backups and recovery

VaultRun runs on **one host**. The API, the docker guard, Postgres, Redis, the dashboard and every sandbox share it. There is no failover: this page says what that means, how to back up and restore, and what to promise the people who rely on it.

## Deployment model

- Give VaultRun a **dedicated host or VM**. The docker guard holds the Docker socket, which is root on the host, and sandboxes run code nobody reviewed; nothing else should run there.
- Size it for your peak of concurrent sandboxes (CPU and memory limits per session add up), plus Postgres. Put the workspaces on their own disk with `make xfs-workspace`.
- Postgres can live elsewhere (a managed database with its own replication and backups): set `DATABASE_URL` and drop the `postgres` service.

## When the host fails

| | Lost | Kept when the host comes back | Restored from a backup on a new host |
|---|---|---|---|
| Running sandboxes and in-flight runs | yes | no: sessions are closed at startup (their container is gone) | no |
| Live workspace files | | yes, until the session is closed | no |
| Snapshots, promoted artifacts | | yes | yes |
| Sessions, runs and their output, API keys, organisations, policies | | yes | yes, as of the backup |
| Audit log, evidence records | | yes | yes, as of the backup; stream the audit log off the host with `AUDIT_EXPORT_URL` to lose nothing |
| Queued async runs (Redis) | may be | yes (Redis saves every 60 s) | no |

Clients see errors while the host is down and get new sessions afterwards. Agents should treat a sandbox as disposable: snapshot work that must survive (`POST /api/v1/sessions/:id/snapshots`) and keep results as artifacts.

## Backups

```bash
make backup                       # writes backups/<UTC time>/
BACKUP_DIR=/mnt/backup make backup
```

A backup holds:

- `postgres.dump`: a consistent `pg_dump` of the database;
- `files.tar.gz`: snapshots and artifacts;
- `env`: your `.env` with every key the database depends on (audit HMAC, evidence signing, database and master key).

Live session workspaces are not included: a restored session is closed anyway, because its sandbox is gone.

The backup contains every secret of the installation, so **encrypt it and keep it off the host**. For example, nightly at 02:15 with [age](https://age-encryption.org):

```cron
15 2 * * * cd /opt/vaultrun && make -s backup BACKUP_DIR=/var/backups/vaultrun && \
  tar -C /var/backups/vaultrun -cf - . | age -r age1yourkey... > /mnt/offsite/vaultrun-$(date +\%F).tar.age && \
  rm -rf /var/backups/vaultrun/*
```

**Test a restore** after you set this up, and then every quarter. A backup you have not restored is a hope, not a backup.

## Restore

On the same host after data loss, or on a new one:

```bash
git clone https://github.com/nickvd7/vaultrun && cd vaultrun   # the version the backup came from
make harden                       # new host: gVisor and the workspace disk
make restore BACKUP=/path/to/backups/20261030T021500Z
make up
```

- **`.env`:** `make restore` uses the backup's `.env` when there is none. An existing `.env` must match the backup's (the keys belong to the database), unless you set `RESTORE_KEEP_ENV=1`.
- **Database:** it is replaced by the dump.
- **Files:** snapshots and artifacts go back into the workspace directory.
- **After `make up`:** existing API keys work again, `GET /api/v1/audit/integrity` checks the restored log, and sessions that were running when the backup was made are closed.

How long this takes is mostly the time to provision the host. The restore itself takes minutes for a database of a few GB.

## Monitoring

- `GET /health`: the database, Docker (through the guard) and the job queue. Alert when it is not `ok` for a minute.
- `GET /metrics` (Prometheus, `METRICS_TOKEN`): active sessions, container creation failures, run durations.
- The disk holding the workspaces and Docker's data (`/var/lib/docker`). With `WORKSPACE_QUOTA=xfs` a session cannot fill it, but many sessions together can.
- `GET /api/v1/audit/integrity`, which the API also checks every five minutes, logging `audit: LOG INTEGRITY COMPROMISED`.
- The guard's log (`docker compose logs docker-guard`). A `docker guard: refused` line means something asked Docker for more than VaultRun needs. That should never happen in normal operation, so investigate it.

## Upgrades

```bash
make backup
git pull && make up               # migrations run at startup
```

Sandboxes keep running across `make up`. Sessions created before an upgrade keep their old sandbox settings until they are recreated.

## Higher availability

What you can do today:

- a managed or replicated Postgres (`DATABASE_URL`, read replicas with `DATABASE_READ_URL`);
- a cold standby host restored from the latest backup;
- the audit log streamed off the host (`AUDIT_EXPORT_URL`).

[multi-region.md](multi-region.md) describes active-passive setups. Running sessions do not move between hosts: a standby starts with new sandboxes.

## What to promise customers

If you run VaultRun for others, say so in your agreement. Suggested wording, to adapt with your lawyer:

> The service runs on a single server without automatic failover. If that server becomes unavailable, sandboxes that are running and commands in progress are lost and must be started again; files a customer saved as snapshots or artifacts, the audit log and account data are restored from the most recent backup. Backups are made every [24 hours] and kept for [30 days] off the server, so data written in the [24 hours] before a failure may be lost. We aim to restore service within [4 business hours] of a failure. Planned maintenance is announced [48 hours] in advance.

Fill in the numbers you actually achieve: the restore time is the time from noticing the failure to `make up` on a new host, measured in your last restore test.
