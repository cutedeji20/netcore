# Production admission gates

This directory records the conditions for enabling the NetCore control plane.
It is intentionally stricter than a successful container build or a working
UI deployment.

## Kamatera control-plane package

`compose.yaml` is the first production-safe control-plane package. It creates
one private Docker network with a fixed Caddy address (`172.30.0.2`) and exposes
only Caddy's TCP 80/443 ports. Caddy terminates HTTPS and proxies `/api`,
`/auth`, and `/webhooks` to the private API service; every other path goes to
the UI. The API trusts forwarded client addresses only from that fixed Caddy
container address.

The package includes:

- a locked admin UI, private API and worker containers running non-root with
  read-only filesystems;
- Redis protected by a mounted password, with no published port;
- PostgreSQL with a private generated CA, TLS certificate verification, SCRAM
  login roles and no published port;
- a tracked migration runner that records applied versions before a later
  deployment can safely be repeated; and
- Caddy automatic certificates and HSTS at the public edge.

Docker is not installed in the current development workspace, so the Compose
package must be validated with `docker compose config` and a non-production
Kamatera staging host before it is used against live data.

### Runtime secret layout

Copy `.env.example` to `.env` and set only the public hostname and an absolute
runtime directory. Do not put a secret in `.env`. Create this tree on the
Kamatera host with permissions that allow only the exact container user to read
each mounted directory:

```text
${NETCORE_RUNTIME_DIR}/
  app/
    db_dsn
    netcore.json
    bootstrap_admin_password     # temporary; only for first bootstrap
    bootstrap_totp_code          # temporary; only for first bootstrap
  redis/
    redis_password
  postgres/
    postgres_bootstrap_password
    postgres_owner_password
    postgres_api_password
    postgres_radius_password
  migration/
    migration_pg_service.conf
  radius/
    db_password                  # same value as postgres_radius_password
    clients.conf                 # rendered, secret-bearing FreeRADIUS clients
```

`app/db_dsn` must use the `netcore_api` login and certificate verification,
for example with `sslmode=verify-full` and
`sslrootcert=/run/netcore/postgres-tls/ca.crt`. `redis/redis_password` must be a
non-empty base64url value. `migration_pg_service.conf` is a libpq service file
for the `netcore_owner` account and also names that CA path; it is a secret
because it contains the owner password. `netcore.json` is the SOPS-decrypted
runtime SecretStore document and is never committed.

Before the first administrator ceremony, `netcore.json` must contain a
dedicated Base32 TOTP secret under a logical key, for example:

```json
{"auth.mfa.initial_admin":"BASE32_AUTHENTICATOR_SECRET"}
```

Generate and retain that secret through the encrypted secret-management
workflow, then add it to the intended administrator's authenticator app. Do
not put the secret in Compose, `.env`, a shell command, a ticket, or a database
row. The two `bootstrap_*` files are short-lived local inputs, not permanent
runtime secrets.

The PostgreSQL init script uses the bootstrap-only `netcore_bootstrap` superuser
to create the protected, non-superuser `netcore_owner` migration owner plus the
low-privilege `netcore_api` and `netcore_radius_login` login roles from their
separate password files. The migration job later grants the login roles only the existing
`netcore_app_rw` and `netcore_radius` capability roles. The migration owner
has `BYPASSRLS` solely because it owns the narrow `SECURITY DEFINER` database
functions; its connection service file must never be mounted into application
containers.

`postgres/postgres_radius_password` and `radius/db_password` contain the same
non-empty base64url database password but are separate mounts: the former is
owned by UID/GID `70:70` with mode `0400` for PostgreSQL initialization, and
the latter is owned by UID/GID `101:101` with mode `0400` for FreeRADIUS.
Rotate the pair together. Never loosen either file's mode merely to make two
containers share it.

### Dashboard-managed Resend and Paystack credentials

Resend and Paystack keys are not mounted into the API, worker, UI, `.env`, or
`netcore.json`. A privileged administrator enters each key through **Settings
→ Integrations** using a current password and authenticator code. NetCore sends
a confirmation message to that administrator for Resend and makes a read-only
Paystack balance request before accepting the record. The database retains only
an AES-GCM encrypted envelope; the data-encryption key is wrapped by a
versioned, non-exportable Azure Key Vault key.

Before enabling this capability, set only these public configuration values in
the production `.env`:

```text
NETCORE_INTEGRATION_CRYPTO_BACKEND=azure-key-vault
NETCORE_INTEGRATION_KEK_ID=https://<vault>.vault.azure.net/keys/netcore-integrations-kek/<version>
```

The Azure VM's **system-assigned managed identity** must receive only the
`Key Vault Crypto Service Encryption User` role on that individual key. The
vault must use a private endpoint and private DNS zone; public network access
is disabled. An unavailable key vault fails configuration, checkout, receipt
delivery and webhook signature verification closed. It never falls back to a
file key or environment secret.

`radius/clients.conf` is a rendered FreeRADIUS file, not the tracked template.
It contains exactly one `client` block per active database NAS, using the NAS
source address and its independently generated shared secret. It is a secret:
set it to owner/group `101:101` with mode `0400`. Never put a broad subnet,
`0.0.0.0/0`, a development `testing123` secret, or a disabled NAS into this
file. The database independently checks that the NAS is active before it can
write accounting data.

### First staged start

On a non-production Kamatera host with DNS already pointing at the server and
TCP 80/443 allowed by the host firewall:

```bash
cp deployments/production/.env.example deployments/production/.env
# Edit NETCORE_DOMAIN and NETCORE_RUNTIME_DIR only.

docker compose -f deployments/production/compose.yaml build
docker compose -f deployments/production/compose.yaml up -d postgres-tls postgres redis
docker compose -f deployments/production/compose.yaml --profile maintenance run --rm migrate
docker compose -f deployments/production/compose.yaml up -d
```

Run `docker compose -f deployments/production/compose.yaml config` before the
first `up`, and inspect the API, worker, PostgreSQL and Redis logs. A migration
failure is a stop condition; never delete a production data volume to make it
start. Back up and restore-test PostgreSQL before applying any migration to a
live environment.

### FreeRADIUS durable spool and replay (staging first)

FreeRADIUS is intentionally in the separate `radius` Compose profile. It is
not started by the normal control-plane command, and it must not be connected
to a customer router until the acceptance steps below have succeeded on a
non-production NAS.

The `radius` service accepts RADIUS requests only for declared NAS devices. It
writes every accepted accounting request to the host-mounted detail spool before
acknowledging it. `radius-replay` reads that spool and calls the narrowly scoped
database function. A PostgreSQL outage therefore creates a visible backlog
instead of silently losing usage. Packet replay remains safe because the
database deduplication key and quota watermark are authoritative.

1. Create `${NETCORE_RADIUS_SPOOL_DIR}` on a dedicated, monitored host
   filesystem. It must be writable by UID/GID `101:101`, have capacity above
   `NETCORE_RADIUS_SPOOL_MAX_BYTES + NETCORE_RADIUS_SPOOL_MIN_FREE_BYTES`, and
   not share the root filesystem. The default 5 GiB spool plus 1 GiB free-space
   reserve is a starting limit, not an estimate of your final capacity. The
   profiled `radius-spool-init` service sets the top-level directory to `0700`
   and UID/GID `101:101`; it deliberately does not repair existing packet files.
2. Render `${NETCORE_RUNTIME_DIR}/radius/clients.conf` from the active NAS
   records and secret manager. The NAS address must exactly match the database
   `nas.nasname`; render secrets once, review the diff without printing secret
   values, then set file owner/group `101:101` and mode `0400`.
3. Restrict the host firewall so UDP 1812 and 1813 accept traffic only from
   those router source addresses over the private VPN. Do not publish the port
   range to the public internet. TCP is not exposed and UDP 3799 is not
   published because this phase does not yet originate CoA/Disconnect packets.
4. Build and validate both FreeRADIUS configurations on staging:

   ```bash
   docker compose -f deployments/production/compose.yaml --profile radius build radius radius-replay
   docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius
   docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius-replay
   docker compose -f deployments/production/compose.yaml --profile radius up -d radius radius-replay
   ```

5. Capture evidence for every one of these staging tests before enabling a
   live router:

   - expired, wrong-MAC and replayed portal nonce are rejected; an active
     handoff returns only the expected RADIUS reply attributes;
   - unregistered source and disabled NAS are rejected, both at the client
     declaration and by database ingestion;
   - Start, Interim and Stop create the expected session/accounting rows; an
     exact retransmit does not add a record or quota delta;
   - stop PostgreSQL or block only the replay container's database access,
     send accounting, and verify the detail spool grows while the NAS receives
     an Accounting-Response; restore PostgreSQL and verify the spool drains and
     each event is applied once;
   - reduce the spool ceiling temporarily, prove the writer logs
     `event=radius_spool_capacity_exceeded` and stops rather than acknowledging
     unpersistable requests, while `radius-replay` continues draining; and
   - restart the writer and replay services with a nonempty spool and verify
     no packet is lost or double-applied.

Alert on the capacity event, writer/replay restart loops, replay service
unavailability, spool age exceeding two accounting interim intervals, and host
free space falling below the configured reserve. The FreeRADIUS 3.2.10 image is
pinned because it includes an upstream detail-listener reader fix; any image
update requires rerunning this procedure.

### First privileged administrator (one-time local ceremony)

Run this only after all migrations finish successfully and before changing the
UI from locked to live. It has no HTTP route and refuses to run if any tenant
or user already exists. The command connects with the migration-owner service
file mounted only into this maintenance container; the normal API role cannot
create tenants or grant every permission.

1. Write a unique, 16+ character administrator password to
   `${NETCORE_RUNTIME_DIR}/app/bootstrap_admin_password`, and the current
   six-digit code from the already-paired authenticator to
   `${NETCORE_RUNTIME_DIR}/app/bootstrap_totp_code`. Ensure only the non-root
   container user and authorised host operators can read the files.
2. Run the local maintenance command. Neither the password nor the TOTP code
   appears in the command line, environment, database, or audit metadata:

   ```bash
   docker compose -f deployments/production/compose.yaml --profile maintenance run --rm bootstrap \
     -tenant-name "Example Network" \
     -tenant-slug example-network \
     -timezone Africa/Lagos \
     -currency NGN \
     -email admin@example.com \
     -password-file /run/netcore/runtime/bootstrap_admin_password \
     -totp-secret-ref auth.mfa.initial_admin \
     -totp-code-file /run/netcore/runtime/bootstrap_totp_code
   ```

   The supplied code is stored as consumed, so wait for the next authenticator
   period before the first browser sign-in. A second invocation is rejected.
3. Remove both `bootstrap_*` files with the host's approved secret-disposal
   procedure. Retain the encrypted sources in the authorised secret manager,
   not the plaintext runtime input files.
4. Change the production `.env` to `NETCORE_UI_MODE=live` and set
   `NETCORE_TENANT_SLUG` to the exact bootstrap slug, then recreate the UI:

   ```bash
   docker compose -f deployments/production/compose.yaml up -d --force-recreate ui
   ```

   The dashboard sends the email, password and current authenticator code to
   the same-origin API. A session cookie is created only after all three are
   accepted; a missing, invalid, replayed or unavailable MFA factor creates no
   session. Every page and action still receives server-side permission checks.
   Operator sessions are absolute, rather than extended by activity. The Compose
   package uses an eight-hour maximum by default through
   `NETCORE_AUTH_SESSION_TTL`; the dashboard locks itself at the server-provided
   expiry and the API rejects older sessions even if they were issued before a
   shorter policy was deployed.

## Current safe state

The control dashboard is **locked by default**. It does not render sample
records, customer information, billing values, network state, or operational
controls until it can verify `GET /api/v1/me` through the same HTTPS origin.
The dashboard only enables that flow when the UI process is started with:

```text
NETCORE_UI_MODE=live
NETCORE_TENANT_SLUG=<lowercase-tenant-slug>
```

Those values contain no credential. They must only be set on the private
control-plane deployment where the edge proxy serves the UI and proxies both
`/api` and `/auth` to the API service. Do not set them for the Railway static
UI deployment: Railway remains a locked review surface until the control plane
is moved behind the production edge. The included Compose package defaults the
UI to locked mode; enabling live mode is a separate, reviewed change after the
MFA bootstrap succeeds.

## Required production topology

```text
Operators -- HTTPS --> Caddy edge -- UI
                                  \-- API -- PostgreSQL
                                           \-- Redis
Routers -- private VPN/firewall --> FreeRADIUS -- durable detail spool/replay
```

Only Caddy exposes TCP 80/443 to the public internet. PostgreSQL, Redis, API,
worker and FreeRADIUS management interfaces stay on the Docker/private network.
RADIUS UDP ports are restricted to registered router source addresses over the
private VPN or firewall rules.

## Non-negotiable go-live gates

All items below must be complete and evidenced before enabling a live customer
router or Paystack:

1. A same-origin HTTPS Caddy deployment, a production API and worker, and a
   PostgreSQL connection with certificate verification. No API port is public.
2. Encrypted runtime secrets plus the dashboard-managed envelope design:
   repository files, environment files and image layers contain no passwords,
   Resend keys, Paystack secret keys, RADIUS shared secrets, database keys or
   MFA secrets; the Key Vault KEK is private-network-only and least-privilege.
3. Schema migrations and SQL invariants pass against the production candidate,
   with a tested backup and restore procedure.
4. An operator bootstrap procedure creates the first tenant, administrator
   role and active TOTP device without a default password. MFA-required
   privileged login is enforced before any browser session is created.
5. FreeRADIUS uses rendered per-NAS client secrets, least-privilege database
   access, a disk-backed accounting detail spool, replay listener, disk quota
   and alerts. Test Start, Interim, Stop, retransmit, database outage and
   replay before connecting a live router.
6. Paystack stays disabled until the dashboard's test-key validation succeeds,
   webhook verification/idempotency is exercised in a test account, and a
   failed or delayed callback cannot activate a subscription.
7. Monitoring, alert routing, audit-log retention, host patching, backup
   restoration, incident contacts and an explicit rollback procedure are
   verified.

## Current delivery boundary

The UI hardening, MFA-enforced login, one-time privileged operator bootstrap,
and first Kamatera package prevent the public Railway address from acting as an
unauthenticated admin panel and provide a secure staging control plane. They do
**not** make the whole ISP control plane production-ready by themselves.
Audited write workflows, a Compose validation run, FreeRADIUS router acceptance
tests, monitoring/alert routing and live Paystack acceptance remain blockers
for real customer Wi-Fi and payments. The dashboard may be enabled only after
the bootstrap ceremony; customer access and Paystack remain disabled.
