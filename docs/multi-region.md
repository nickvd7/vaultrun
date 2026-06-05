# VaultRun Multi-Region Deployment

VaultRun is designed to run in a single region by default, but supports multi-region
deployments for high availability and data locality. This guide covers the two main
patterns: **active-passive** (failover) and **active-active** (geo-distributed).

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `REGION` | _(empty)_ | Region identifier included in `/health` responses and audit logs (e.g. `us-east-1`) |
| `DATABASE_READ_URL` | _(empty)_ | DSN of a read replica; when set, list/get queries are routed there |

```bash
# Primary (us-east-1)
REGION=us-east-1
DATABASE_URL=postgres://vaultrun:secret@primary-db.us-east-1.internal:5432/vaultrun

# Optional read replica (same region, reduces primary load)
DATABASE_READ_URL=postgres://vaultrun:secret@replica-db.us-east-1.internal:5432/vaultrun
```

The `/health` endpoint will include the region:

```json
{
  "status": "ok",
  "region": "us-east-1",
  "checks": { "database": {"status": "ok"}, "docker": {"status": "ok"} }
}
```

---

## Active-Passive (Recommended Starting Point)

One primary region handles all writes; a standby region runs a warm replica and
can be promoted if the primary fails.

```
┌─────────────────────────────────────────────────────────────┐
│ Primary Region (us-east-1)                                  │
│                                                             │
│  ┌──────────┐   ┌──────────┐   ┌──────────────────────┐   │
│  │ API (×2) │──▶│ Redis    │   │ PostgreSQL (primary)  │   │
│  └──────────┘   │ (primary)│   └──────────────────────┘   │
│                 └──────────┘         │ streaming repl       │
└───────────────────────────────────── │ ─────────────────────┘
                                        │
┌────────────────────────────────────── │ ─────────────────────┐
│ Standby Region (eu-west-1)            ▼                      │
│                                                             │
│  ┌──────────┐   ┌──────────┐   ┌──────────────────────┐   │
│  │ API (×2) │   │ Redis    │   │ PostgreSQL (replica)  │   │
│  │(read-only│   │ (replica)│   │  DATABASE_READ_URL    │   │
│  │   mode)  │   └──────────┘   └──────────────────────┘   │
│  └──────────┘                                               │
└─────────────────────────────────────────────────────────────┘
```

### PostgreSQL Streaming Replication

On the primary:
```sql
-- postgresql.conf
wal_level = replica
max_wal_senders = 5
wal_keep_size = 1GB

-- pg_hba.conf (allow replica user from standby)
host  replication  vaultrun_repl  <standby-ip>/32  scram-sha-256
```

On the standby:
```bash
pg_basebackup -h primary-db -U vaultrun_repl -D /var/lib/postgresql/data -P -R
```

Then set `DATABASE_READ_URL` on the standby API instances to point to the local replica.

### Failover Procedure

1. Promote the replica: `pg_ctl promote -D /var/lib/postgresql/data`
2. Update `DATABASE_URL` in the standby region's `.env` to point to the newly promoted primary
3. Update DNS / load balancer to route traffic to the standby region
4. Point `REGION` to the new region name

---

## Active-Active (Advanced)

Both regions accept writes. Requires either:
- **Citus** (distributed PostgreSQL) for sharding across regions, or
- **CockroachDB** (distributed SQL with multi-region support), or
- Application-level sharding (route sessions by user/org to a specific region)

### Recommended Setup with CockroachDB

```bash
# Region 1
DATABASE_URL=postgresql://vaultrun@cockroachdb-us:26257/vaultrun?sslmode=verify-full
REGION=us-east-1

# Region 2
DATABASE_URL=postgresql://vaultrun@cockroachdb-eu:26257/vaultrun?sslmode=verify-full
REGION=eu-west-1
```

CockroachDB's multi-region table localities pin data close to users:
```sql
ALTER DATABASE vaultrun SET PRIMARY REGION "us-east-1";
ALTER DATABASE vaultrun ADD REGION "eu-west-1";

-- Pin session workspaces to the region where they were created
ALTER TABLE sessions SET LOCALITY REGIONAL BY ROW;
```

---

## Docker Compose Multi-Region Example

`deployments/docker-compose.region.yml` can be used as an overlay:

```yaml
# docker-compose.region.yml — overlay for a specific region
# Usage: docker compose -f docker-compose.yml -f docker-compose.region.yml up
version: "3.9"
services:
  api:
    environment:
      - REGION=${REGION:-us-east-1}
      - DATABASE_READ_URL=${DATABASE_READ_URL:-}
  frontend:
    environment:
      - NEXT_PUBLIC_API_URL=${PUBLIC_API_URL:-http://localhost:8080}
```

---

## Session Affinity

Docker sandboxes are local to the host — a session created in `us-east-1` cannot
be accessed from `eu-west-1` because the container and workspace files live on that
host. Options:

1. **DNS-based affinity**: Route clients to the same region using latency-based DNS
   (Route 53, Cloudflare). This is the simplest approach.

2. **Session proxy**: A global API tier routes `/sessions/:id/*` requests to the
   correct region based on `sessions.region` stored in a shared DB.

3. **Workspace replication**: After a session stops, zip the workspace and upload it
   to a shared S3 bucket (`ARTIFACT_S3_BUCKET`). The next region can restore from
   the snapshot.

---

## Redis Multi-Region

VaultRun uses Redis for:
- Distributed rate limiting (`REDIS_ADDR`)
- Async run queue (when `REDIS_ADDR` is set)

For multi-region, options:
- **Redis Sentinel**: automatic failover within one region
- **Redis Cluster**: horizontal sharding (supported by `go-redis/v9`)
- **Upstash / Redis Enterprise**: managed global replication

In active-passive, the standby region can point to the primary Redis until failover.

---

## Health Monitoring

With `REGION` set, your monitoring stack can identify which region is degraded:

```bash
# Check all regions
for region in us-east-1 eu-west-1 ap-southeast-1; do
  curl -s https://api-${region}.example.com/health | jq '{region: .region, status: .status}'
done
```

Prometheus multi-region scrape config:
```yaml
scrape_configs:
  - job_name: vaultrun
    static_configs:
      - targets: ['api-us-east-1.example.com:8080']
        labels: { region: 'us-east-1' }
      - targets: ['api-eu-west-1.example.com:8080']
        labels: { region: 'eu-west-1' }
    metrics_path: /metrics
    bearer_token: ${METRICS_TOKEN}
```
