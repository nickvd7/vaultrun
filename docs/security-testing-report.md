# Security testing report — v0.3.0 features

**Date:** 2026-07-31
**Scope:** the six features added in v0.3.0 (session replay, browser automation, cost intelligence, natural-language policy, session templates, multi-agent collaboration)
**Method:** targeted unit tests per attack class, plus an end-to-end suite run against a real PostgreSQL 16 instance

This report supersedes an earlier version that described intended controls rather than implemented ones. Everything below is backed by a test in the repository; the "Verified by" column names it. Where a control is absent, that is stated rather than omitted.

## Summary

Eleven defects were found and fixed. Nine are security-relevant, two were correctness bugs that made a feature unusable. Six of the security defects were exploitable with nothing more than an ordinary API key.

| # | Defect | Impact | Fixed in |
|---|--------|--------|----------|
| 1 | Rego injection through the policy explanation | Restrictive policy compiles to allow-all | `internal/nlpolicy/validate.go` |
| 2 | iptables injection through allowed hosts | Sandbox gains unrestricted egress | `internal/nlpolicy/validate.go` |
| 3 | Checkpoint HMAC covered only identity fields | Recorded command, exit code and output rewritable | `internal/replay/manager.go` |
| 4 | Cost checksum covered only derived totals | Usage evidence behind a charge rewritable | `internal/cost/tracker.go` |
| 5 | SSRF filter missing most private ranges | Sandbox reaches link-local, CGNAT, IPv6 private | `internal/browser/manager.go` |
| 6 | Python literal escaping incomplete | Arbitrary Python in the browser sandbox | `internal/browser/manager.go` |
| 7 | `cost_metrics` deletable | Billing record erasable | `migrations/015` |
| 8 | WebSocket accepted any `Origin` | Cross-site session hijack | `cmd/api/handlers/collab.go` |
| 9 | `replay_enabled` not enforced by the API | Workspace snapshot of a session that never opted in | `internal/replay/manager.go` |
| 10 | Checkpoint creation always failed | Feature unusable | `internal/replay/manager.go` |
| 11 | Cost summary always failed | Feature unusable | `internal/cost/types.go` |

Two classes of input validation were also absent entirely and have been added: template fields (`internal/templates/validate.go`) and collaboration fields (`internal/collab/validate.go`).

## Findings

### 1. Rego injection through the policy explanation

`CompileToOPA` wrote the LLM-produced explanation into a Rego comment by interpolation:

```go
rego.WriteString(fmt.Sprintf("# %s\n\n", policy.Explanation))
```

A newline ends the comment, so the remainder of the value is parsed as policy code. A prompt that steered the model toward an explanation of `Safe policy\nallow { true }` produced a module with an unconditional allow rule, defeating every other rule in the same policy. `\r`, CRLF and `default allow = true` worked equally well.

This is the most serious finding: the natural-language policy engine exists to let non-experts write security policy, and its output is the thing enforcing that policy. The input reaching this field is attacker-influenced by design — that is what a natural-language interface is.

**Fix:** `Policy.Validate` rejects newlines and control characters in the explanation, and is called at the top of all three compile methods. **Verified by:** `TestRegoInjectionViaExplanation`.

### 2. iptables injection through allowed hosts

`CompileToNetworkRules` built rules the same way:

```go
fmt.Sprintf("iptables -A OUTPUT -d %s -j ACCEPT", host)
```

An allowed host of `1.1.1.1 -j ACCEPT; iptables -P OUTPUT ACCEPT` appends a rule that replaces the default DROP, granting the sandbox unrestricted egress. Command substitution (`$(…)`, backticks), pipes and embedded newlines were also effective.

**Fix:** allowed hosts must match a hostname, IP address or valid CIDR block; shell and iptables metacharacters, URLs and paths are rejected. **Verified by:** `TestIptablesInjectionViaAllowedHosts` (18 payloads) and `TestAllowedHostsAcceptsLegitimateValues` (11 legitimate values, to confirm the filter is not merely restrictive).

### 3. Checkpoint HMAC covered only identity fields

`signCheckpoint` hashed four fields:

```go
data := fmt.Sprintf("%s:%s:%d:%d",
    cp.SessionID, cp.WorkspaceSnapshotID, cp.CheckpointNumber, cp.CreatedAt.Unix())
```

The command, arguments, exit code, duration, captured stdout and stderr, archive path, size and environment snapshot were all unauthenticated. Anyone able to write to `replay_checkpoints` could rewrite what a session was recorded as having done while the signature still verified — which is precisely the scenario the signature exists to detect.

**Fix:** the HMAC covers every immutable field, NUL-separated so a character cannot be shifted across a field boundary. Optional fields hash a placeholder when unset so "absent" and "empty" are distinguishable. `verifyCheckpoint` now fails closed when no signing key is configured. **Verified by:** `TestSignatureCoversExecutionContext` (16 tamper cases, each of which passes verification under the old scheme) and `TestReplayTamperDetection`, which performs the tampering through SQL against a real database.

### 4. Cost checksum covered only derived totals

`checksum` hashed the session ID, the period and three of the four cost figures. The raw usage counters — CPU core-hours, memory GB-hours, GPU hours, the three storage figures, egress and ingress — plus `network_cost` and `rate_id` were unprotected. The evidence justifying a charge could be rewritten while the digest continued to match.

Floats were formatted with `%f`, which truncates at six decimals, so two distinct sub-microdollar amounts produced the same digest.

**Fix:** all counters, all four cost figures and the rate card are covered; floats use `strconv.FormatFloat('g', -1, 64)`; timestamps are normalised to UTC so a metric does not fail verification because Postgres returned it in a different location. Added `Tracker.VerifyMetric`, which checks both the checksum and the HMAC and fails closed without a key. **Verified by:** `TestChecksumCoversUsageCounters`, `TestChecksumDistinguishesSmallAmounts`, `TestVerifyMetricRejectsRecomputedChecksum`.

### 5. SSRF filter missing most private ranges

`isPrivateIP` checked three CIDR blocks: `10/8`, `172.16/12` and `192.168/16`. Absent were loopback beyond the separate `IsLoopback` check, link-local (`169.254/16` — which contains the cloud metadata endpoints), carrier-grade NAT (`100.64/10`, routable inside many hosting networks), `0.0.0.0/8`, the reserved ranges, and every IPv6 private range (`::1`, `fc00::/7`, `fe80::/10`).

The metadata check covered only `169.254.169.254`, missing the AWS ECS task endpoint at `169.254.170.2`. Metadata hostnames such as `metadata.google.internal` were not blocked by name, so a provider changing its address, or answering only from inside the VPC, would defeat the address check.

`validateURL` also resolved the hostname before checking the scheme, and accepted a host whose first resolved address passed even if a later one was private.

**Fix:** all of the above ranges are covered, parsed once at init; metadata hostnames are blocked by name; the scheme is checked before resolution; every resolved address must pass. **Verified by:** `TestSSRFProtection` (28 payloads), `TestIsPrivateIP` (28 boundary values including the off-by-one edges of each range), `TestSSRFAllowsPublicURLs`.

**Remaining risk:** DNS rebinding. Validation resolves the hostname, then the browser resolves it again independently; a record with a one-second TTL can answer differently for the two lookups. Closing this requires pinning the validated address for the connection, which the current Docker-exec design cannot express. The container's own network policy is the mitigating control.

### 6. Python literal escaping incomplete

`escapeString` replaced single quotes only:

```go
return strings.ReplaceAll(s, "'", "\\'")
```

Browser operations are executed by generating a Python script and running it in the container, with selectors and form values interpolated into single-quoted literals. A backslash in the input consumed the escape of the following quote; a raw newline terminated the literal and let the remainder be parsed as Python statements.

**Fix:** backslashes are doubled first, then quotes, newlines, CR, tab and NUL are escaped. **Verified by:** `TestEscapeStringPreventsInjection`, which scans the output and fails if any character capable of terminating the literal appears unescaped, rather than checking for specific payloads.

### 7. `cost_metrics` could be deleted

Migration 012 created a `BEFORE UPDATE` trigger. `DELETE` was unguarded, so the cheapest way to hide usage was to remove the row rather than edit it — and an append-only audit trail that permits deletion is not append-only.

**Fix:** migration 015 extends the trigger to `BEFORE UPDATE OR DELETE`, while still allowing the `ON DELETE CASCADE` from `sessions` (distinguished by checking whether the parent session still exists). The same protection is added to `replay_checkpoints`, which the application never updates either, making tampering impossible rather than merely detectable for anyone holding ordinary table privileges. **Verified by:** `TestCostMetricsAreImmutable`, `TestCheckpointRowsAreImmutable`.

### 8. WebSocket upgrades accepted any Origin

```go
CheckOrigin: func(r *http.Request) bool {
    // TODO: Check origin against allowed origins
    return true
},
```

Any page a user visited could open a collaboration WebSocket against their VaultRun instance with their credentials attached, then read messages and drive the session.

**Fix:** the origin is checked against `CORS_ALLOWED_ORIGINS`. A request with no `Origin` header is allowed, because non-browser clients (the SDK, agents) do not send one and browsers always do for WebSocket handshakes — so this does not weaken the check for the case it defends.

### 9. `replay_enabled` not enforced by the API

The runner hook checked the flag; the HTTP handler did not. A checkpoint captures the workspace and the environment, so recording one for a session that never opted in is a data-retention problem, not just wasted storage.

**Fix:** the check moved into `Manager.CreateCheckpoint`, so neither entry point can skip it. **Verified by:** `TestReplayRejectsDisabledSession`.

### 10 and 11. Two features did not work at all

`getNextCheckpointNumber` ran `SELECT MAX(checkpoint_number) … FOR UPDATE`, which PostgreSQL rejects: *FOR UPDATE is not allowed with aggregate functions*. Every checkpoint creation returned 500. The surrounding `SERIALIZABLE` transaction also committed before the insert, so it protected nothing even had the query been valid.

`SessionCostSummary` and `OrgCostSummary` carried no `db:` tags, so sqlx failed with *missing destination name session_id* on every cost summary request. `FirstMetric` and `LastMetric` were also non-pointer `time.Time`, which cannot hold the NULL that `MIN`/`MAX` return for a session with no metrics.

Neither could be caught without a real database, which is why both survived the unit suite. `models.Session` was likewise missing all seven columns added by migrations 011–014, which broke `SELECT s.*` and made `GET /api/v1/sessions` return 500 for every non-master caller; and migration 012 referenced a non-existent table `orgs` instead of `organizations`, so it never applied to a fresh database.

## Isolation testing

Cross-tenant and cross-session isolation was tested end to end, since an authorisation check that exists in one handler and not another is a common failure.

| Property | Verified by |
|----------|-------------|
| Checkpoints are not readable through another session's URL | `TestReplayCrossSessionIsolation` |
| Checkpoints are not restorable into another session | `TestReplayCrossSessionIsolation` |
| A non-member cannot list, read, restore, fork or delete another org's checkpoints | `TestReplayCrossTenantIsolation` |
| A non-member cannot read another org's costs | `TestCostCrossTenantIsolation` |
| Every checkpoint route rejects a missing and an invalid key | `TestReplayRequiresAuthentication` |
| Every template route rejects a missing key | `TestTemplateRequiresAuthentication` |

## Input validation

Neither templates nor collaboration validated their input. Both now do.

**Templates** (`internal/templates/validate.go`): image references reject URL schemes, path traversal, shell metacharacters, whitespace and NUL, while still accepting `registry:port/path:tag` and `@sha256:` digests. Resource limits reject zero and negative values and cap at 64 CPU / 256 GB / 24 h. Allowed hosts reject `*`, URLs and paths. Environment variable names must be POSIX identifiers; values are deliberately unrestricted, since a secret or a JSON blob is legitimate. Slugs must be lowercase kebab-case, because they appear as URL path segments. List sizes and the startup script are bounded. `Validate` reports every problem at once and surfaces as 400 rather than 500. **Verified by:** 60 cases in `internal/templates/validate_test.go`, including `TestBuiltInTemplatesPassValidation`, which fails if a shipped template would be rejected from the API.

**Collaboration** (`internal/collab/validate.go`): agent IDs are interpolated into Redis keys of the form `collab:session:<uuid>:agent:<id>`, so `:` and glob characters are rejected to prevent collisions with sibling keys in the same namespace. Message bodies are bounded at 64 KB and must be valid UTF-8. Message types and agent statuses are restricted to their known values. `max_agents` is capped at 32, since each agent costs a connection and two goroutines. **Verified by:** 50 cases in `internal/collab/validate_test.go`.

The WebSocket handler also reported rejections *after* `upgrader.Upgrade`, where `c.JSON` can no longer write a response. All validation now runs before the upgrade, and a failed upgrade releases the agent slot instead of leaking it until the Redis TTL expires.

## Robustness

| Property | Verified by |
|----------|-------------|
| Malformed UUIDs, SQL fragments, traversal sequences and NUL bytes in paths produce 4xx, never 5xx | `TestReplayRejectsMalformedIDs` |
| Truncated, wrongly-typed, deeply nested and invalid-Unicode JSON produce 4xx | `TestNewEndpointsSurviveMalformedJSON` |
| A 10 MB field is rejected | `TestNewEndpointsRejectOversizedBodies` |
| Template bootstrap is idempotent across restarts | `TestTemplateBootstrapIsIdempotent` |
| Checkpoint numbering is per-session and gapless | `TestReplayCheckpointNumbersAreSequential` |
| All 15 migrations apply forward and roll back | manual run against PostgreSQL 16 |

## What remains

These are known and unaddressed. They are recorded here rather than in a "future work" section because each is a live limitation of the shipped code.

**DNS rebinding in browser navigation.** Described under finding 5.

**Redis is trusted.** Agent presence and pub/sub payloads are read back without authentication or integrity checking. An attacker with Redis access can forge presence and inject broadcast messages. Redis is assumed to be on a private network; there is no defence if it is not.

**No rate limiting on the new endpoints.** Checkpoint creation performs a tar+gzip of the workspace, and each browser operation starts a Python process in the container. Both are expensive enough that an authenticated caller can degrade the host by calling them in a loop. The existing per-actor session quota does not cover either.

**LLM policy output is validated, not verified.** `Policy.Validate` ensures the policy compiles safely; it cannot tell whether the policy means what the user asked for. A model that misunderstands "block outbound email" produces a syntactically valid policy that does not block it. The compiled Rego is returned to the caller for review, which is the only mitigation.

**File synchronisation is last-write-wins.** Concurrent edits by two agents silently discard one side's changes. `file_versions` records what happened but nothing acts on it.

**Signing keys are not rotatable.** Checkpoints and cost metrics are signed with `AUDIT_HMAC_KEY`. Changing it invalidates every existing signature, since no key ID is stored alongside them.

## Reproducing

Unit tests, including all security suites:

```bash
go test ./...
```

End-to-end suite (needs PostgreSQL; provide an empty database, the suite runs the migrations):

```bash
INTEGRATION_DSN="postgres://user:pass@localhost:5432/vaultrun_test?sslmode=disable" \
  go test -tags=integration ./...
```

Without `INTEGRATION_DSN` the suite starts a container through testcontainers, which needs a working Docker socket.

## Reporting a vulnerability

See [SECURITY.md](../SECURITY.md). Private advisories through GitHub, or mail@030.dev with `[SECURITY]` in the subject.
