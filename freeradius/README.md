# FreeRADIUS configuration

This directory is version-controlled production configuration. The Kamatera
package builds it into two separate FreeRADIUS configurations: a UDP writer
that journals accounting packets to disk first, and a detail listener that
replays that journal into PostgreSQL. The `radius` Compose profile remains
disabled by default until a rendered NAS client file and staging acceptance run
are complete.

## Portal authorization

`policy.d/netcore_portal` accepts only the 43-character portal nonce sent by
the RouterOS `http-pap` login. Its one SQL call invokes
`radius_portal_handoff_authorize(nonce, nas_ip, client_mac)`, which atomically:

1. consumes the short-lived, MAC- and NAS-bound nonce;
2. confirms the subscription is still active; and
3. returns the RouterOS rate, timeout, quota and filter reply attributes.

No result is an Access-Reject. Do not replace this with separate nonce and
plan/quota queries: the nonce is single-use and must not be evaluated twice.

`mods-enabled/netcore_sql` connects as `netcore_radius`. That role has no
direct access to customers, payments, plans, subscriptions, portal handoffs,
or usage counters. It also cannot call the inner nonce-consumer function: the
complete one-call authorization function is its only entitlement surface.

## Before enabling on a router

1. Apply database migrations `0028_radius_portal_access_policy` and
   `0030_radius_accounting_ingest`.
2. Use the `netcore_radius_login` **LOGIN** principal created by the production
   PostgreSQL init script. It is a member of the `netcore_radius` capability
   role; the password is read from the mounted
   `radius/db_password` file at process start, never from Compose. It must be
   the same non-empty base64url value as the PostgreSQL-only
   `postgres/postgres_radius_password` file, but it is mounted separately so
   it can remain readable only by the FreeRADIUS UID.
3. Render `clients.conf.template` once for every registered NAS from the secret
   store. Never write the rendered RADIUS shared secret to Git or derive it
   from `nas.secret_ref`.
4. Mount `mods-enabled/netcore_sql`, `policy.d/netcore_portal`,
   `policy.d/netcore_accounting`, and the rendered client file into the active
   FreeRADIUS paths. Add
   `netcore_portal_handoff` to the HotSpot virtual server's `authorize`
   section and its `Auth-Type Accept { accept }` block to `authenticate`; add
   `acct_unique` to `preacct` and `netcore_accounting` to `accounting`.
5. Use the Kamatera `radius` and `radius-replay` services. The detail writer
   acknowledges only after writing to `/var/lib/netcore-radius/spool`; the
   replay listener retries database failures, and the writer stops when its
   capacity guard reaches the configured ceiling.
6. Keep CoA source restrictions and router firewall controls enabled beside
   these policies. The example Compose override is opt-in only.
7. Validate the assembled configuration with `radiusd -XC` in the target
   image, then test an expired/replayed token, a wrong NAS, a wrong MAC, an
   active metered plan, an unmetered plan, Start/Interim/Stop retransmits, and
   Accounting-On/Off against a non-production NAS.

RouterOS provisioning files are in `routeros/hotspot/`. They use `http-pap`
because the one-use handoff is returned in a RouterOS login URL; switching to
`http-chap` requires a separate client-side CHAP design. The script leaves the
HotSpot profile on `radius-interim-update=received`, allowing the policy's
60- or 300-second `Acct-Interim-Interval` to take effect.

The same script enables RouterOS's incoming Disconnect/CoA listener on UDP
3799 and inserts a router-input firewall allow rule for exactly the rendered
NetCore control-plane address, followed by a drop for every other source. Do
not substitute a guest or broad office subnet for `__COA_SOURCE_ADDRESS__`.
See `routeros/hotspot/README.md` for the local-page and acceptance workflow.

For a plan configured with `REDIRECT` after quota exhaustion, the script also
creates the `netcore-quota-exhausted` HotSpot filter chain. It permits only the
configured portal address and blocks other traffic. This is intentionally a
safe portal-only state rather than an HTTPS interception attempt; verify the
renewal experience on the target browser and router before using that plan
action in production.

## Accounting ingestion

`policy.d/netcore_accounting` uses the generated
`Acct-Unique-Session-Id`, packet timestamp, both octet directions, and both
Gigaword attributes. It calls only `radius_accounting_ingest(...)`. The
database function verifies the active NAS plus `Class=netcore:<subscription>`
issued during authorization, creates or closes the session, inserts the raw
accounting record, and advances the quota watermark in one transaction.

A duplicate packet is a successful no-op: it neither creates another session
nor charges usage a second time. An unknown/disabled NAS, malformed Class, or
counter total that cannot fit the signed database representation is refused.
`Accounting-On` and `Accounting-Off` are stored separately and close sessions
that existed at the event time.

The ingestion policy plus the Kamatera spool/replay package form the durability
path; production readiness still requires the documented staging evidence,
host disk monitoring, router firewall restrictions, and alerts for spool
capacity or replay failure. Until those are evidenced, a PostgreSQL failure is
not a live-router acceptance condition.
