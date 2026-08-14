# FreeRADIUS configuration

This directory is version-controlled production configuration. The base
Compose service does **not** yet enable these files: it mounts this directory
under `/etc/raddb/local` only. That is intentional until a real RADIUS client
file, a database login, a durable accounting spool, and a test NAS are deployed.

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
2. Provision a least-privilege **LOGIN** principal that is a member of
   `netcore_radius`; inject its password as `RADIUS_DB_PASSWORD`. The schema
   role itself stays `NOLOGIN`.
3. Render `clients.conf.template` once for every registered NAS from the secret
   store. Never write the rendered RADIUS shared secret to Git or derive it
   from `nas.secret_ref`.
4. Mount `mods-enabled/netcore_sql`, `policy.d/netcore_portal`,
   `policy.d/netcore_accounting`, and the rendered client file into the active
   FreeRADIUS paths. Add
   `netcore_portal_handoff` to the HotSpot virtual server's `authorize`
   section and its `Auth-Type Accept { accept }` block to `authenticate`; add
   `acct_unique` to `preacct` and `netcore_accounting` to `accounting`.
5. Add a durable `detail` writer and its separate replay listener before
   acknowledging accounting during a PostgreSQL outage. The exact file path,
   disk quota, replay process, and alerting belong to the target deployment
   rather than this portable template.
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

This is an ingestion policy, not a complete durability claim. Do not consider
quota enforcement production-ready until the target image has a disk-backed
detail spool plus replay listener, bounded disk retention, and alerts for spool
write/replay failure. Without it, a PostgreSQL failure must remain visible and
unacknowledged so the NAS retries; it must never become a silent dropped packet.
