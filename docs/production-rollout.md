# Staff and customer production rollout

This procedure deploys the access-and-customer foundation to the production
control plane. It is an operator-run, non-destructive release: it applies the
tracked migration, recreates only the API, worker, and UI, and uses disposable
test identities. Do not run it from a development workstation or against a
host whose backup/restore gate has not passed.

## Stop conditions and preflight

Before making a release change, an authorised operator must record:

- a current PostgreSQL backup and a successful restore test for the production
  candidate; no migration is attempted without both;
- review of the pending fast-forward and deployment configuration, including a
  public, fragment-free HTTPS `NETCORE_STAFF_INVITE_URL` on the exact dashboard
  origin;
- Azure Key Vault readiness: the VM system-assigned managed identity has only
  `Key Vault Crypto Service Encryption User` on the versioned key configured by
  `NETCORE_INTEGRATION_KEK_ID`, and the private endpoint/private DNS path is
  usable; and
- a dedicated, disposable staff mailbox and a unique test customer email. Do
  not use an existing operator or customer account.

Do not place passwords, TOTP secrets, invitation tokens, Resend credentials,
or Key Vault identifiers in shell history, a ticket, or test evidence. Stop on
any failed preflight item, failed migration, unhealthy service, or unexpected
customer/staff data; investigate or follow the approved incident/rollback
procedure rather than deleting volumes, rows, sessions, or audit history.

## Required command order

On the approved Azure production host, run these commands in this exact order.
They are intentionally shown for an operator to execute; this document does
not authorise automatic production execution.

```bash
cd /srv/netcore/src
git pull --ff-only
docker compose -f deployments/production/compose.yaml --profile maintenance run --rm migrate
docker compose -f deployments/production/compose.yaml build api worker ui
docker compose -f deployments/production/compose.yaml up -d --force-recreate api worker ui
docker compose -f deployments/production/compose.yaml ps
curl -fsS --max-time 15 -o /dev/null -w '%{http_code}\n' https://hotspot.durabledatahubs.com/
```

`git pull --ff-only` prevents an implicit merge. The migration is the first
release action and is a hard stop if it fails. The recreate command deliberately
names only `api`, `worker`, and `ui`; it does not recreate PostgreSQL, Redis,
Caddy, or any RADIUS-profile service. In the `ps` output, API, worker, and UI
must be running and every declared health check must be healthy. The final curl
must return `200`, confirming the public dashboard UI is reachable over HTTPS.
If either service health or the UI status differs, stop before any browser
smoke test.

## Browser smoke checklist

Use a private browser profile, fresh test data, and normal same-origin HTTPS
access. Capture only redacted result evidence.

1. Sign in as an Administrator with the administrator's existing MFA factor.
   Confirm the API/UI are usable and that **Settings → Integrations** reports
   Resend as **Active**. Do not enter, rotate, reveal, or test provider keys
   during this release.
2. Invite the disposable staff mailbox with its intended fixed role. The
   Administrator supplies current password and fresh MFA proof. Confirm the
   received link uses `staff-invite.html` with its token in the fragment, not a
   query string; do not copy the token into evidence.
3. In a separate private browser profile, open that link. The recipient chooses
   their own password, completes TOTP enrolment, and signs in normally with the
   invited role. Confirm only the permissions appropriate to that role are
   available. The Administrator must never see the recipient password or TOTP
   secret.
4. As the Administrator, deactivate the disposable staff account with fresh
   password and MFA proof. In the recipient profile, make a request or refresh
   the dashboard and confirm the session is rejected. Keep the deactivated
   record and its audit history; do not delete it.
5. Sign in as an Operations staff member. Create a customer with the unique
   disposable email and ordinary profile fields only (first name, last name,
   email, and optional phone). Do not create a portal password or credentials
   from the staff dashboard.
6. In a separate customer browser profile, register and verify a portal account
   using that exact customer email. Confirm the verified portal account links to
   the pre-existing customer profile rather than creating a duplicate. Do not
   begin checkout or buy a plan.
7. Return to the Operations session and deactivate the test customer. Confirm
   the profile is retained with a deactivated status and audit history. This
   release does not suspend subscriptions, terminate service, or alter router
   state; do not perform those actions to test deactivation.

Any failure in a permission boundary, invite lifecycle, MFA enrolment, session
revocation, duplicate protection, or portal linking is a release stop. Preserve
the redacted evidence and escalate through the approved release process.

## Explicit exclusions

This rollout and smoke test must not:

- create a Paystack checkout, charge, refund, validate a live payment, or
  change Paystack credentials;
- write router/NAS configuration, activate/suspend a subscriber through a
  router, or issue a CoA/Disconnect; or
- start, rebuild, reconfigure, validate, or send traffic to FreeRADIUS or its
  replay/spool services.

Those systems require their own approved acceptance and rollout procedures.
