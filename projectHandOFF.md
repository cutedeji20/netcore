# NetCore Project Handoff

**Project:** NetCore ISP and captive portal platform  
**Repository:** `https://github.com/cutedeji20/netcore.git`  
**Primary checkout:** `C:\laragon8.6.1\www\netcore`  
**Production checkout:** `/srv/netcore/src` on `netcoreVM-01`  
**Handoff date:** 17 September 2026  
**Current branch:** `main`  
**Latest local and remote commit:** `9f3e69c Preserve empty RADIUS filter field`

## 1. Executive status

The payment, subscription activation, portal sign-in, and automated RouterOS
handoff paths are implemented. The final RADIUS authorization issue was traced
to the policy reply serializer dropping a NULL `filter_id`, which produced seven
fields while the FreeRADIUS policy correctly requires eight. Commit `9f3e69c`
preserves that empty value as `-`.

This change is committed and the focused portal test passes, but it still needs
production deployment and a single clean physical Wi-Fi sign-in acceptance test.
Do not regard RADIUS access as accepted until the RouterOS active-session list
shows an `R` session and the client has normal internet access.

The customer registration screen currently cannot send email verification codes.
This is a separate Resend integration/delivery issue and does not require a
RouterOS change. It remains unconfirmed whether the cause is a missing/inactive
Resend credential, invalid sender domain, rejected key, or network/provider
delivery failure.

## 2. Architecture and live network facts

### Application services

Production runs with Docker Compose from:

```text
/srv/netcore/src/deployments/production/compose.yaml
```

Relevant services:

| Service | Responsibility | Notes |
| --- | --- | --- |
| `api` | Public portal, dashboard API, handoff issuance, payment verification | Healthy API is required for portal login and payment confirmation. |
| `ui` | Dashboard and portal static assets | Existing running UI must serve the current portal JavaScript. |
| `worker` | Async payment/receipt processing | Not needed for RADIUS policy-only deployment. |
| `postgres` | Primary database and migrations | PostgreSQL is readiness-critical. |
| `redis` | Rate limits, ephemeral login/OTP state | Do not clear globally in production. |
| `radius` | FreeRADIUS writer listening on UDP 1812/1813 | Production process must use the normal entrypoint so its database password file is loaded. |
| `radius-replay` | Replays durable accounting spool entries | Deploy together with `radius`. |

### Router and NAS identity

The identity used by the browser is deliberately different from the identity
seen in real RADIUS packets:

| Field | Confirmed value | Purpose |
| --- | --- | --- |
| HotSpot address | `192.168.88.1` | Browser captive-portal login address. |
| Router RADIUS source address | `10.254.77.2` | Source IP and `NAS-IP-Address` in RouterOS Access-Request packets over WireGuard. |
| Azure RADIUS listener | `172.16.0.4:1812/1813` | Docker-published RADIUS endpoint. |
| NAS database record | `nasname=10.254.77.2`, `hotspot_address=192.168.88.1`, `radius_source_ip=10.254.77.2`, `status=ACTIVE` | This is the required canonical mapping. |

The RouterOS packet evidence confirms that `NAS-IP-Address` is `10.254.77.2`.
Changing the NAS record back to `192.168.88.1` as `nasname` will break portal
handoff authorization.

## 3. Completed work

### 3.1 DNS and payment-page access

- Router DNS was repaired and tested with a successful ping to `1.1.1.1` and a
  successful resolution of `neverssl.com`.
- RouterOS DNS remote requests are enabled for HotSpot clients.
- The Squad hosted payment page was made reachable through the HotSpot walled
  garden.
- Required payment domains added during troubleshooting include:
  - `pay.squadco.com`
  - `api.squadco.com`
  - `api-d.squadco.com`
  - `popup.squadco.com`
  - `checkout.squadco.com`
- Payment initiation was successfully tested through NetCore Wi-Fi after the
  walled-garden additions.

### 3.2 Squad payment verification and subscription activation

- Squad payment links now work on direct mobile data and through the HotSpot.
- Payment verification endpoint was corrected to use:

  ```text
  GET /transaction/verify/{reference}
  ```

- Payment activation SQL type errors were corrected. Prior errors included
  undetermined PostgreSQL parameter types while queueing activation/payment
  success events.
- Successful manual verification returned a response containing:
  - payment and subscription IDs
  - `status: SUCCESS`
  - plan start and expiry timestamps
  - `already_activated: false`
- The current observed paid plan was `Phone Daily`, with a one-day active
  subscription. This is evidence that payment-to-subscription activation works.

### 3.3 Portal customer authentication and automatic HotSpot handoff

- Customer login succeeds in the portal.
- The portal now automatically posts the captive-portal context to:

  ```text
  POST /api/v1/portal/handoff
  ```

- Portal JavaScript captures these RouterOS query values:
  - `client_mac`
  - `nas_address`
  - `hotspot_login_url`
- A successful handoff response is HTTP `201` and provides a one-use redirect
  URL to RouterOS with a 43-character nonce and password `portal-handoff`.
- The browser no longer requires the user to manually paste console handoff
  code for the normal journey.
- Relevant implementation:
  - `cmd/ui/assets/portal.js`
  - `internal/portal/http.go`
  - `internal/portal/portal.go`
  - `internal/portal/store_postgres.go`

### 3.4 RADIUS plan enforcement

The RADIUS path now enforces subscription-based access instead of granting
internet merely because a user has logged into the website.

- HotSpot profile was configured to use RADIUS with accounting and interim
  updates.
- The policy accepts only a 43-character portal handoff nonce plus the sentinel
  password `portal-handoff`. It does not treat this sentinel as a normal user
  password.
- `radius_portal_handoff_authorize` consumes a nonce atomically and returns the
  RADIUS reply policy in the same transaction.
- The database now enforces:
  - active, unexpired subscription
  - correct tenant, NAS, and client MAC binding
  - one-time nonce use
  - plan `max_devices`
  - plan `max_concurrent_sessions`
  - plan rate limit
  - session timeout, idle timeout, and accounting interim interval
  - metered byte limits where the plan has one
- Temporary rows in `radius_access_reservations` prevent a race where multiple
  near-simultaneous logins can exceed the plan's concurrency limit.
- Reservation lifetime is 120 seconds. A debug or test request that reaches the
  SQL function consumes the handoff and creates the reservation even when a
  later policy error causes the final Access-Reject.

### 3.5 RADIUS defect fixes already committed

| Commit | Change | Outcome |
| --- | --- | --- |
| `ed38699` | Enforce RADIUS plan device and session limits | Introduced database-backed device/concurrency enforcement. |
| `bacc583` | Separate hotspot and RADIUS NAS identities | Added separate local HotSpot and RADIUS source identity support. |
| `4156978` | Continue hotspot sign-in after portal login | Restored automatic browser handoff after portal authentication. |
| `c438dff` | Match NAS identity to RouterOS RADIUS packets | Uses `10.254.77.2` for actual RADIUS authorization. |
| `33d0ee6` | Fix RADIUS plan authorization query | Fixed ambiguous `subscription_id` SQL error. |
| `bbcc7ce` | Fix RADIUS portal reply validation | First repair of policy-reply validation. |
| `f30e3d0` | Use safe RADIUS portal reply delimiter | Switched reply serialization away from pipe parsing. |
| `6c1738e` | Simplify RADIUS portal reply parsing | Reduced brittle reply parsing. |
| `b694399` | Match RADIUS portal reply from request list | Makes the policy match the generated temporary reply in FreeRADIUS 3.2. |
| `9f3e69c` | Preserve empty RADIUS filter field | Coalesces NULL `filter_id` to `-`, preserving all eight required reply fields. |

### 3.6 Evidence from the final RADIUS debugging cycle

A valid authorization query returned:

```text
f04c0883-66a6-4641-b6ff-690d11d743cd,2000000/2000000,14400,600,300,-,-
```

This proves all of the following were valid at the SQL authorization boundary:

- portal nonce format
- active paid subscription
- NAS mapping
- client MAC binding
- plan access policy
- rate, session, idle, and interim limits

The prior rejection was caused by serialization of a NULL `filter_id`: PostgreSQL
`concat_ws` removes NULL values. The reply became seven fields instead of eight.
Commit `9f3e69c` changes the final expression to:

```sql
COALESCE(filter_id, '-')
```

The expected response shape is now exactly eight comma-separated values.

## 4. Current required deployment

### 4.1 Deploy the latest RADIUS policy

The local branch and `origin/main` both point to `9f3e69c`. On Azure, deploy:

```bash
cd /srv/netcore/src
git pull --ff-only

docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  --profile radius up -d --build --force-recreate radius radius-replay
```

`api` and `worker` do **not** need recreation for this policy-only change.

Confirm the version and services:

```bash
git log -1 --oneline

docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  ps radius radius-replay
```

Expected commit:

```text
9f3e69c Preserve empty RADIUS filter field
```

### 4.2 Normal acceptance procedure

1. Ensure no temporary `radiusd -X` debug container is still running.
2. Wait at least two minutes after the most recent failed handoff attempt. This
   allows a stale reservation to expire.
3. Forget/disconnect the Netcore Wi-Fi network on the test device.
4. Reconnect and complete portal sign-in once.
5. Do not run browser-console handoff code or a debug RADIUS process during
   this acceptance attempt.
6. Check RouterOS:

   ```routeros
   /ip hotspot active print detail
   /radius monitor 0 once
   ```

Acceptance criteria:

- `accepts` increases.
- `rejects` does not increase for the fresh attempt.
- `/ip hotspot active print detail` shows an active `R` RADIUS session.
- The device loads a normal external HTTPS website without captive-portal
  interception.

### 4.3 If the acceptance attempt fails

Do not retry repeatedly, as each valid handoff can create a temporary access
reservation. Capture one debug trace only.

Stop normal RADIUS first:

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml stop radius
```

Start the password-safe debug server:

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  run --rm --service-ports --entrypoint sh radius -lc '
    set -eu
    radius_password=$(tr -d "\r\n" < /run/netcore/runtime/radius_db_password)
    umask 077
    export PGPASSFILE=/tmp/netcore-radius.pgpass
    printf "%s:%s:%s:%s:%s\n" "$RADIUS_DB_HOST" "$RADIUS_DB_PORT" "$RADIUS_DB_NAME" "$RADIUS_DB_USER" "$radius_password" > "$PGPASSFILE"
    chmod 600 "$PGPASSFILE"
    unset radius_password
    exec /opt/sbin/radiusd -X -d /opt/netcore-radius/writer
  '
```

Make exactly one fresh portal sign-in attempt, capture the `Access-Request`
through `Access-Accept` or `Access-Reject`, then stop debugging with `Ctrl+C`.
Restore normal RADIUS immediately:

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  --profile radius up -d radius radius-replay
```

Never enable `suppress_secrets=no` and never paste a RADIUS shared secret,
database password, Resend key, or payment secret into a ticket or chat.

## 5. RouterOS configuration status

### Known required HotSpot profile settings

The HotSpot profile `hsprof1` must have RADIUS enabled and use PAP for the
portal handoff:

```routeros
/ip hotspot profile set [find where name="hsprof1"] \
  login-by=http-pap,mac-cookie \
  use-radius=yes \
  radius-accounting=yes \
  radius-interim-update=received
```

No router reboot is required after this setting or the RADIUS policy deployment.
For testing, disconnect and reconnect the client instead.

### Current behavioral policy

The observed `Phone Daily` plan has:

| Plan control | Current value | Practical meaning |
| --- | --- | --- |
| `max_devices` | `1` | Only one registered device MAC at a time. |
| `max_concurrent_sessions` | `1` | Phone A and Phone B cannot use the plan simultaneously. |
| Session timeout | `14400` seconds | Each RADIUS session is limited to four hours. |
| Idle timeout | `600` seconds | Session disconnects after ten minutes idle. |
| Interim update | `300` seconds | Router reports accounting every five minutes. |
| Rate limit | `2000000/2000000` | Approximate 2 Mbps up/down RouterOS rate limit. |

If the intended commercial policy is "one user may move freely between devices
but may use only one at a time", keep `max_concurrent_sessions=1` and decide
whether `max_devices` should be more than one. If the intended policy is shared
simultaneous access, raise both limits deliberately in the plan configuration.
This is a product decision, not a RouterOS troubleshooting change.

## 6. Payment and subscription operational status

### Completed

- Squad hosted checkout is available from Wi-Fi after walled-garden setup.
- Server-side payment verification succeeds.
- Successful payment activates the subscription.
- The admin dashboard can display successful payment/subscription state after
the payment queue fixes.

### Pending operational validation

- Confirm payment receipts are delivered after Resend is correctly configured.
- Re-run one end-to-end paid signup only after customer registration email
verification is operational.
- Do not create additional test payments solely to troubleshoot RADIUS. The
existing active subscription is sufficient for RADIUS acceptance testing.

## 7. Customer registration email verification issue

### Symptom

On the portal registration screen, a new customer enters an email, Nigerian
phone number, and password, then sees:

```text
We could not send a verification code. Please try again shortly.
```

The recipient address is not the issue merely because it is Gmail. The public
portal uses the tenant's configured Resend credential and sender identity.

### Design

The API resolves the Resend credential just before sending. It deliberately
fails closed and does not reveal configuration details to a public user.

Relevant code:

- `cmd/api/main.go` - `configuredAccountHTTP`
- `internal/auth/accounts.go` - registration and OTP issuance
- `internal/auth/accounts_http.go` - public registration endpoint
- `internal/notify/resend.go` - tenant Resend delivery
- `internal/integrations/resolver.go` - encrypted tenant credential resolver

### Likely causes

1. No active Resend integration exists for tenant slug `data-hub`.
2. The stored Resend credential cannot be decrypted/resolved.
3. The Resend API key is invalid, expired, or revoked.
4. The sender email has not been verified in Resend.
5. Resend rejected the delivery request or the API could not reach Resend.

### Safe diagnosis

After reproducing the registration attempt once, run:

```bash
cd /srv/netcore/src

docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  logs --since 3m api --tail=200 | grep -Ei 'tenant Resend|credential|delivery|registration'
```

The safe log values are:

| `failure_stage` | Meaning | Next action |
| --- | --- | --- |
| `credential_resolution` | Integration missing/inactive/unreadable or sender invalid | Configure/reconnect Resend for `data-hub`. |
| `transport_unavailable` | API cannot reach Resend | Check server DNS, egress, and provider availability. |
| `provider_rejected` with provider status | Resend rejected the sender or key/request | Inspect Resend dashboard and verify sender domain/key. |

Check integration status without printing credentials:

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  exec -T postgres \
  psql -U netcore_bootstrap -d netcore -c "
SELECT t.slug, i.provider, i.status, i.sender_email,
       i.last_tested_at, i.last_test_succeeded
FROM integration_providers i
JOIN tenants t ON t.id = i.tenant_id
WHERE t.slug = 'data-hub' AND i.provider = 'resend';"
```

### Required resolution

Configure Resend in the NetCore dashboard under the tenant integration settings,
separately from Squad payment settings:

- use a valid Resend API key;
- configure a sender such as `DataHub <access@durabledatahubs.com>`;
- verify `durabledatahubs.com` (or the sender's domain) in Resend;
- use the integration test/verification operation in the dashboard;
- never enter or save the Resend key in source control or a shell history file.

## 8. Database migrations and production schema state

Important migrations for this rollout:

| Migration | Purpose | Required status |
| --- | --- | --- |
| `0043_radius_plan_access_limits` | Device/session enforcement and temporary access reservations | Applied before RADIUS acceptance. |
| `0044_nas_hotspot_address` | Adds separate captive-portal address | Applied. |
| `0045_restore_nas_ip_address` | NAS migration compatibility | Applied as part of history. |
| `0046_restore_radius_nas_identity` | Restores actual RouterOS RADIUS identity | Applied. |
| `0047_fix_radius_plan_authorization_sql` | Resolves ambiguous subscription SQL reference | Applied. |

Verify the two critical later migrations:

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  exec -T postgres \
  psql -U netcore_bootstrap -d netcore -c \
  "SELECT version FROM netcore_schema_migrations WHERE version IN ('0046','0047') ORDER BY version;"
```

Expected:

```text
0046
0047
```

## 9. Useful production commands

### Deployment status

```bash
cd /srv/netcore/src
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml ps
```

### RADIUS health and recent logs

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  logs --since 10m radius radius-replay --tail=300
```

### API payment and portal errors

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  logs --since 10m api --tail=300 | grep -Ei 'payment|verify|portal|handoff|error|failed'
```

### Clear only portal login rate-limit keys for controlled testing

Use only when login attempts are rate-limited. This does not fix RADIUS or
email delivery issues.

```bash
docker compose --env-file deployments/production/.env \
  -f deployments/production/compose.yaml \
  exec -T redis sh -lc '
    export REDISCLI_AUTH="$(cat /run/netcore/runtime/redis_password)"
    redis-cli --scan --pattern "auth:portal-login:*" | xargs -r redis-cli DEL
  '
```

Do not flush Redis or delete unrelated keys.

## 10. Items explicitly not to do

- Do not reboot the MikroTik as a routine response to a portal/RADIUS test.
- Do not repeatedly submit portal handoffs while debugging; use one fresh login
  after the two-minute reservation window.
- Do not run manual browser console handoff code in normal acceptance tests.
- Do not set `suppress_secrets=no` in FreeRADIUS.
- Do not copy credentials from the dashboard, containers, `.env`, or runtime
  files into chat, logs, Git, screenshots, or this document.
- Do not modify NAS identity values without packet evidence. The actual RADIUS
  identity is `10.254.77.2`; the HotSpot browser address is `192.168.88.1`.
- Do not create a new payment merely to test RADIUS while an active plan exists.
- Do not reset or flush the database/Redis to troubleshoot a single customer.

## 11. Priority ordered next actions

1. Deploy commit `9f3e69c` to `radius` and `radius-replay`.
2. After the two-minute reservation window, run exactly one clean Wi-Fi portal
   sign-in and capture RouterOS active/RADIUS monitor output.
3. If accepted, test external internet access and then test the configured plan
   device/concurrent-session limits deliberately.
4. Diagnose the Resend registration failure using the safe API log and
   integration-status queries above.
5. Configure/repair the tenant Resend integration and verify an OTP is
   delivered before accepting new-customer registration.
6. Perform one complete real-user journey: register, verify email, sign in,
   choose plan, pay, activate subscription, hand off to RouterOS, browse.
7. Decide and document commercial device policy for every plan. In particular,
   decide whether a subscriber can register multiple devices while retaining a
   one-session-at-a-time restriction.

## 12. Acceptance checklist

### RADIUS access

- [ ] Latest RADIUS commit deployed.
- [ ] NAS record has the confirmed three values.
- [ ] FreeRADIUS sends Access-Accept for one fresh portal handoff.
- [ ] RouterOS displays a live `R` HotSpot session.
- [ ] Client receives usable internet access.
- [ ] Accounting updates arrive at the configured interval.
- [ ] Concurrent device/session limits behave according to the selected plan.

### Customer registration and payment

- [ ] Tenant Resend integration is active and tested.
- [ ] Sender domain is verified in Resend.
- [ ] New customer receives registration OTP.
- [ ] Customer can verify email and sign in.
- [ ] Checkout opens through captive portal.
- [ ] Successful payment activates subscription.
- [ ] Receipt delivery is verified, if enabled.

### Operational hygiene

- [ ] No debug RADIUS container remains running.
- [ ] `radius` and `radius-replay` are both healthy/running.
- [ ] No secrets have been logged or committed.
- [ ] Production notes are updated after the physical acceptance test.
