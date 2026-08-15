# NetCore ISP Platform

Commercial-grade WISP / ISP management and AAA platform.

**Status:** Phase 0 + Phase 1 complete; Phase 2 identity/RBAC foundations are in progress. See [Phase report](#phase-report) below.

Engineering contract: `BUILD.md` v1.1 (+ v1.2 corrections, see [Deviations](#deviations-from-buildmd)).

---

## Quick start

```bash
# 5-container core: postgres, redis, freeradius, api, worker (§107)
make dev

# Apply schema and prove every invariant holds
make migrate
make test-db

# Everything CI runs
make verify
```

Requires Docker, Go 1.25+, and `psql`.

---

## What Phase 1 contains

| Area | Where | Verified by |
|---|---|---|
| Database schema + invariants | `db/migrations/` | `tests/invariants.sql` — 52 checks against live PostgreSQL 16 |
| Quota accounting (§21A) | `internal/quota`, `quota_apply()` | Go unit tests + SQL invariant suite |
| Session integrity (§24A) | `sessions` schema, `nas_accounting_events` | SQL invariant suite |
| Money (§101/§102) | `pkg/money` | Unit tests incl. an AST guard banning `float64` |
| Subscription lifecycle (§20) | `internal/subscriptions` | Exhaustive transition matrix test |
| Config safety (§105) | `internal/config` | Production-invariant rejection tests |
| Redacting logger (§46) | `internal/logger` | Leak tests incl. nested groups and `LogValuer` |
| Health (§48) | `internal/health` | Redis-down-stays-in-rotation test |
| Security middleware (§14/§45/§56/§57/§95/§96) | `internal/security` | Log-injection, CORS, 404-not-403 tests |
| RBAC roles + RLS (§10/§60.1) | `0004_roles_rls.up.sql` | Grant assertion tests |
| CI gates (§70) | `.github/workflows/ci.yml` | — |

### The three things most likely to be broken elsewhere

This schema encodes fixes for defects that are easy to ship and expensive to
find. Each is verified by execution, not argument.

1. **Gigawords** (`internal/quota.TotalOctets`) — RADIUS octet counters are
   32-bit and wrap at 4 GiB. Without the high word, a 50 GB plan bills as 2 GB,
   silently. `TestTotalOctets_NaiveVersionIsWrong` proves the naive version is
   wrong so nobody "simplifies" it back.

2. **The accounting dedup key** (`db/migrations/0002`, §9.1) — the obvious
   constraint either cannot be created on a partitioned table, or creates fine
   and deduplicates nothing. Partitioning on `created_at` (insert clock) means
   a retransmit 2s later gets a different key and inserts cleanly. Measured:
   1 duplicated packet → 2 stored rows. Correct version partitions on
   `event_timestamp` and discriminates on `acct_session_time`, both
   packet-derived and stable across retransmits.

3. **`usage_counters.last_applied_watermark NOT NULL DEFAULT '{}'::jsonb`** —
   `jsonb_set(NULL, ...)` returns `NULL` in PostgreSQL. If the column is
   nullable, the watermark never persists, every packet applies against zero,
   and every retransmit double-bills. Measured with the column nullable:
   **2 GiB of real traffic billed as 5 GiB**, watermark still `NULL`.

---

## Repository layout

```
cmd/api          HTTP API entrypoint
cmd/worker       background workers (§98)
internal/quota   §21A — its own module because four subsystems call it
internal/config  §105 — startup safety validation
internal/logger  §46  — redaction as a Handler, not a convention
internal/health  §48  — Redis must not gate readiness
internal/security §14/§45/§56/§57 — middleware
internal/subscriptions §20 — the transition table, in one place
pkg/money        §101/§102 — integer minor units, no float64
db/migrations    schema; read 0002 before changing anything in it
tests/           SQL invariant suite
docs/            Phase 0 documentation set (§111)
freeradius/      version-controlled FreeRADIUS config (§106)
```

---

## Documentation (Phase 0, §111)

| Document | Contents |
|---|---|
| `docs/architecture.md` | Four-plane separation, module boundaries, request lifecycles |
| `docs/security.md` | Control set mapped to OWASP API Top 10 and to code |
| `docs/threat-model.md` | Assets, trust boundaries, attack paths, what is *not* mitigated |
| `docs/database.md` | Schema reference and the invariants the DB enforces |
| `docs/quota.md` | The quota model in full |
| `docs/failure-modes.md` | Per-dependency behaviour table (§81/§82) |
| `docs/events.md` | Event catalogue, transports, idempotency per consumer |
| `docs/deploy-railway.md` | Railway UI-preview and later API/worker deployment guide |
| `docs/deploy-hostinger-vps.md` | Superseded Hostinger VPS reference; shared hosting is unsupported |
| `docs/deploy-kamatera.md` | Selected Kamatera production-control-plane deployment guide |
| `docs/production-rollout.md` | Combined Kamatera + Railway production-readiness runbook and go-live gates |

---

## Deviations from BUILD.md

Declared per §129 and §136. Nothing here was changed silently.

### v1.2 corrections applied to the spec

These were found by adversarial review and by executing the schema. The
migrations implement the corrected design; `BUILD.md` should be updated to
match.

| # | Spec said | Problem | Fix |
|---|---|---|---|
| 1 | §10 puts `usage_counters` under RLS; §21A.4/§60.1 have FreeRADIUS read it via `rlm_sql` | Incompatible. `rlm_sql` cannot set `app.tenant_id`, so the predicate is NULL, rows filter out, and §82's "quota lookup fails" row fires — **every session on the platform silently gets a 256 MiB budget**, a fail-safe outcome for the wrong reason that would be misdiagnosed as capacity | `radius_quota_lookup()`, `SECURITY DEFINER`. RLS stays on for all application paths; `netcore_radius` has no direct `SELECT` on the table |
| 2 | §9 partitions accounting on `created_at`, unique key includes it | Legal but useless — see [above](#the-three-things-most-likely-to-be-broken-elsewhere) | Partition on `event_timestamp`; discriminate on `acct_session_time` |
| 3 | §9A `last_applied_watermark` nullable | Silently disables all replay protection | `NOT NULL DEFAULT '{}'::jsonb` |
| 4 | §21A.7 reset creates a new counter row | New row has an empty watermark map, so a session spanning the boundary re-applies its **entire** cumulative total. A customer online at midnight on a DAILY plan loses their whole new allowance instantly | `quota_reset()` carries forward watermarks of still-open sessions, prunes closed ones |
| 5 | §21A.8 selects the period implicitly | Selecting by `now()` instead of packet time double-counts at every boundary | `quota_apply()` takes `p_event_time` and selects the containing period |
| 6 | §21A.8 empty `RETURNING` | "No growth" and "no counter row" are indistinguishable; the latter silently discards revenue | `quota_apply()` raises `P0002` for a missing row |
| 7 | §21A.3 `min(2 GiB, 25% of remaining)` | Asymptote: 100 MiB → 25 MiB → 18.75 MiB… the tail of a bundle is never consumable and the customer reconnect-loops | `MinPerSession` floor; below it, grant the whole remainder |
| 8 | §24A.2 Accounting-On/Off | Carry no `Acct-Session-Id`, so they cannot satisfy the accounting dedup key and had nowhere to land | `nas_accounting_events` table |
| 9 | §48 readiness checks Redis | Would drain every instance on a cache outage, defeating §15's per-endpoint policy | Redis reported, never a readiness gate |
| 10 | §9 `sessions.radius_session_id` | One RADIUS attribute under three names across §9/§25/§9.1 | `acct_session_id` everywhere |
| 11 | §47 alert on `gigawords_nonzero == 0` for 24h | Structurally always zero on metered-only deployments (the per-session cap keeps sessions under 4 GiB) — a permanently-firing alert gets silenced, then is quiet when it matters | Synthetic canary is the real guard; the observed counter alerts only where unmetered plans exist |

### Current implementation status

The original runtime limitations are resolved: PostgreSQL/Redis adapters, real
readiness checks, and a separate worker executable are in the tree.

1. **Go 1.24, not 1.25 as §7 requires.** The build environment could not fetch
   the 1.25 toolchain. This is now resolved: the module, CI and container image
   require Go 1.25+.

2. **Runtime adapters are wired.** The original module proxy was unreachable
   in the build environment (`proxy.golang.org` blocked; pgx v5.10 also
   requires Go 1.25). Phase 1 therefore builds on the standard library alone.
   Consequences:
   - PostgreSQL and Redis are now connected via `pgx` and `go-redis`; PostgreSQL
     gates readiness, while Redis remains deliberately non-critical.
   - Prometheus, OpenTelemetry, golang-migrate and River remain future
     additions. Migrations run via `psql` (see the Makefile).
   - Routing uses Go 1.22+ `net/http.ServeMux` method patterns, so no router
     dependency is needed for Phase 1 regardless.

   Nothing about the design blocks this; it is purely an environment
   limitation. Adding the adapters is mechanical and does not change any
   interface defined here.

3. **Not yet implemented** (deliberately out of Phase 1 scope per §135):
   Authentication handlers and RBAC enforcement are in place. Tenant-scoped,
   keyset-paginated read APIs now cover customers, subscriptions, plans, and
   live sessions, billing transactions, router inventory, and voucher batch
   inventory, and team roster; each has
   its own explicit permission and a staff-safe response contract. Still to
   implement: production OTP delivery and SecretStore adapters, password
   recovery/change flows, MFA enrollment and enforcement, role and permission
   administration, customer creation/updates, plan CRUD, live payment-provider
   configuration and webhooks,
   FreeRADIUS/RouterOS integration, metrics/tracing, and actual worker
   consumers.

4. **Phase 2 security foundation (current):** browser sessions are backed by
   PostgreSQL with tenant-scoped RLS, and password hashes use Argon2id. OTP
   challenges are opaque Redis records with a ten-minute lifetime, five total
   verification attempts, one-time consumption, and action binding. TOTP MFA
   supports a one-period clock skew and blocks replay by atomically recording
   the accepted counter. PostgreSQL stores only path-like MFA secret references;
   a production notifier and SecretStore resolver are deliberately required
   before OTP/MFA can be exposed as user-facing endpoints.

---

## Phase report

Per §136.

**1. Summary.** Phase 0 documentation set and Phase 1 foundation: schema with
all database-enforced invariants, quota accounting core, money type, config
validation, redacting logger, health, security middleware, subscription state
machine, Docker, Makefile, CI.

**2. Architecture.** Modular monolith per §6. `internal/quota` is its own
module because four subsystems invoke it (§106). No microservices, no
Kubernetes, no message broker — River replaces RabbitMQ until §36.4 fires
(not yet imported, see limitations).

**3. Files.** 30 migration pairs, Go packages for runtime, identity, OTP,
customer, subscription, plan, payment, session, billing, network, voucher, and team
read APIs, and MFA foundations, 7
Phase 0 docs, CI pipeline,
Compose stack, Makefile, invariant suite.

**4. Database.** `0001` foundation, `0002` sessions/accounting/quota,
`0003` payments/audit/outbox/ledger, `0004` roles/RLS, `0005` identity
sessions/RLS, `0006` TOTP MFA metadata, `0007` customer listing index,
`0008` permission catalogue, `0009` subscription listing index, `0010`
plan permissions, `0011` plan listing indexes, `0012` session permissions,
`0013` session listing indexes, `0014` billing permissions, `0015`
billing listing indexes, `0016` network permissions, `0017` network
listing index, `0018` voucher permissions, `0019` voucher inventory index,
`0020` team permissions, `0021` roster indexes, `0022` Security Center
permission, `0023` Security Center activity index, `0024` automation workflow
catalog, `0025` automation/workspace permissions, `0026` portal handoffs,
`0027` payment activation guards, `0028` RADIUS portal access policy, and
`0029` payment webhook queue, and `0030` narrow RADIUS accounting ingestion.
MFA stores secret references
only and tenant-owned rows are protected by forced tenant RLS.

**5. Security.** Tenant isolation via application predicate + forced RLS on 22
tables; narrow function-only `netcore_radius` grants asserted by test; append-only
audit and subscription history enforced by trigger, not convention; router
management IPs constrained to RFC1918; `*_ref` columns constrained to reject
secret values; payment `SUCCESS` requires `verified_at` and is immutable;
payment request keys are tenant-scoped; browser sessions
persist only SHA-256 token digests; OTP challenges are short-lived and
single-use; TOTP verification has replay protection; deferred ledger balance
trigger; production config refuses to start with wildcard CORS, disabled TLS,
or seed data enabled.

**6. Tests.**

```
Go:  serial test suite, go vet and production build pass locally
SQL: invariant suite includes the session and MFA checks; it still needs a
     PostgreSQL/psql environment to execute locally
```

**7. Commands.**

```bash
gofmt -l .          # clean
go vet ./...        # clean
go test -race ./... # all pass
bash scripts/check-coverage.sh   # all floors met
psql "$DSN" -f tests/invariants.sql   # 31 passed, 0 failed
make migrate && make rollback && make migrate   # reversible
```

**8. Known issues.** Runtime adapters, the first identity/RBAC slice, and
OTP/MFA foundations are now implemented. Delivery/SecretStore adapters,
password recovery, role administration and the business/AAA integrations
remain future work. Production login now requires an active replay-safe TOTP
factor before a browser session is created; the first administrator is created
only through the local one-time bootstrap ceremony documented in
`deployments/production/README.md`. `govulncheck`, `gosec` and
`golangci-lint` remain CI gates and have not been run locally.

**9. Next phase.** Phase 2 — Identity & RBAC (§113): users, Argon2id, sessions,
OTP abstraction, MFA foundation, roles, permissions, tenant middleware, audit
write path and anti-brute-force now have their first implementation slice.
The next delivery slice is a production SecretStore and notifier adapter,
followed by password recovery/change and MFA enrollment/enforcement.

---

## Control dashboard

The dashboard is fail-closed. A new UI server starts in **locked** mode and
does not render representative, customer, billing, network, or operational
data. It can only become live when both of the following are true:

1. `NETCORE_UI_MODE=live` and a valid `NETCORE_TENANT_SLUG` are configured at
   process start; and
2. the browser can establish a same-origin, authenticated API session through
   `/api/v1/me`.

The UI never accepts a browser-supplied API address. The production edge must
serve the UI and reverse-proxy `/api` and `/auth` to the private API service at
the same HTTPS origin. Adapter scripts load only after that session check;
failed and unauthorised requests leave the view empty rather than falling back
to sample records. Server-side permission checks remain authoritative for every
API call.

```powershell
go run ./cmd/ui
```

Open <http://127.0.0.1:3000> to verify the locked state. Do not set the live
environment values on Railway while it hosts only the static UI preview.

## License

Proprietary. All rights reserved.
