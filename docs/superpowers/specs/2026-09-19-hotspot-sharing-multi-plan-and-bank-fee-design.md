# Hotspot Sharing Multi Plan and Bank Fee Design

## Purpose

This change makes NetCore enforce a practical anti-tethering policy at the
RouterOS HotSpot boundary, lets one customer account purchase and manage
multiple independent device subscriptions, and adds a tenant-configurable bank
charge to checkout without changing the value of the purchased plan.

The commercial objective is straightforward: a customer can own separate plans
for separate devices, but cannot use one subscription to provide paid access to
other devices through Android tethering or a concurrent HotSpot sharing setup.

## Scope and success criteria

### In scope

1. Strict TTL-based anti-tethering enforcement for every HotSpot client.
2. Explicit device ownership for subscriptions so one customer can hold more
   than one independent active subscription.
3. Portal checkout that asks which device the new plan is for.
4. A per-tenant fixed NGN bank charge, initially configurable as 1500 kobo
   (N15.00).
5. Immutable payment price breakdowns used for gateway initialization,
   verification, receipts, refunds, reporting, and audit records.
6. Dashboard controls for the configured bank charge and visibility of the
   plan subtotal, bank charge, and customer total.

### Out of scope

- Detecting every possible rooted-device, VPN, custom-TTL, or external-router
  tethering technique. TTL enforcement is a strong practical control, not a
  cryptographic proof of device identity.
- Automatic movement of a paid plan between devices. Device transfer needs a
  future explicit customer-support or customer self-service workflow.
- Percentage provider fees, tax calculation, or provider-specific fee recovery.
- Sharing a single subscription concurrently across devices.

### Acceptance criteria

- A direct HotSpot client can browse after a successful RADIUS handoff.
- A tethered downstream device cannot browse through that client and receives
  the configured blocked/no-access HotSpot behavior.
- One account can buy Plan A for Device A and Plan B for Device B; both plans
  remain independently visible, paid, and enforceable.
- A N500 plan with a N15 configured bank charge creates and verifies a N515
  gateway payment while the subscription price remains N500.
- Existing payments retain their original amount and are never recomputed after
  a bank charge setting changes.
- Only authorized tenant staff can change the charge, and every change is
  audit-logged.

## Current system constraints

- NetCore is a Go service backed by PostgreSQL, Redis, Docker Compose,
  FreeRADIUS, RouterOS HotSpot, and tenant-scoped row-level security.
- Payment amount is server-frozen in `payments.PrepareInitiation`; browsers do
  not submit the amount and gateway verification checks the frozen amount.
- Portal handoff is bound to a RADIUS NAS and client MAC. RADIUS already
  enforces `max_devices` and `max_concurrent_sessions` per subscription.
- The observed RouterOS deployment sends RADIUS packets from `10.254.77.2` and
  redirects browsers to HotSpot address `192.168.88.1`.
- Monetary values are integer minor units. N15.00 is stored as `1500`; N500.00
  is stored as `50000`.

## Design decisions

### 1. Strict TTL anti-tethering policy

RouterOS will classify HotSpot client traffic before it leaves the client
network. Directly connected endpoints normally send packets whose TTL still
matches the expected client value; traffic forwarded by Android tethering has a
TTL one lower. The router will mark likely tethered traffic and deny it using a
dedicated HotSpot forwarding rule.

The router script is idempotent, uses NetCore-specific comments, and is applied
per HotSpot bridge/interface rather than globally. It must:

- leave traffic to the router, DHCP, DNS, portal, payment walled-garden, and
  other management flows untouched;
- inspect only client-originated forward traffic;
- mark packets with a TTL lower than the configured direct-client baseline;
- drop/deny marked packets before they reach WAN forwarding;
- retain counters for operational diagnosis;
- provide a single documented rollback command that disables only NetCore's
  tethering rules;
- be tested on one direct client and one tethered Android device before broad
  rollout.

TTL controls have false-positive risk for unusual network stacks. The admin
configuration exposes an enable/disable switch and an expected TTL baseline,
defaulting to strict mode with the documented RouterOS values. Changes are
audited and require tenant network-write permission.

### 2. Subscription ownership and device binding

The customer account remains the billing owner. Each checkout creates a new
subscription, and each subscription has one required device binding at purchase
time. A device binding contains a normalized MAC, optional customer label, and
timestamps. It belongs to one subscription, not to the customer globally.

The portal changes from "buy a plan" to "choose a plan for a device":

1. The customer chooses an existing registered device or adds the current
   HotSpot MAC as a new device.
2. The server validates the MAC and confirms it is available to that customer.
3. The selected device ID is included in payment initiation, but the server
   still derives all money, plan, customer, and tenant facts.
4. A successful payment activates a subscription bound to that device.
5. Portal handoff and RADIUS authorization must use the subscription bound to
   the connecting MAC. An account may have several active subscriptions, but a
   connection cannot choose another device's subscription.

Each plan retains its own `max_devices` and `max_concurrent_sessions`. For this
release, a device-specific purchase creates a subscription whose effective
device count is one; plans with greater device limits remain useful for a future
bundle/transfer flow but do not weaken the explicit binding model.

The account API and portal display every subscription with device label/MAC,
plan, payment status, start, expiry, quota state, and current session state.

### 3. Bank charge model

Add a tenant billing settings record with:

```text
fixed_bank_charge_minor bigint NOT NULL DEFAULT 0
currency char(3) NOT NULL DEFAULT 'NGN'
updated_at timestamptz NOT NULL
updated_by uuid NOT NULL
```

The setting is restricted to non-negative values. The initial Data Hub setting
will be `1500` minor units. The plan price is never modified.

At initiation, a single transaction locks/reads the active plan and tenant
billing setting, then stores a frozen breakdown on the payment:

```text
plan_amount_minor       = plan.price_minor
bank_charge_minor       = configured fixed fee
amount_minor            = plan_amount_minor + bank_charge_minor
currency                = NGN
```

The payment gateway receives only `amount_minor`, which is the total payable
amount. Gateway verification compares against the total. The subscription event
and entitlement retain the plan amount and do not treat the bank charge as
service value. Receipts and dashboard reporting show the three values
separately.

Changing the charge affects only future payment initiations. Pending payments
retain their frozen breakdown, including when retried through the idempotency
key. Refund/reversal workflows must use the frozen total and breakdown.

### 4. Dashboard and authorization

Add a Billing Settings page/control for tenant staff with `billing.write`
permission:

- fixed bank charge in NGN display units;
- enabled state represented by zero/non-zero charge;
- current effective checkout preview for selected plan prices;
- last updated time and updater;
- confirmation/step-up behavior consistent with existing sensitive integration
  and payment settings;
- append-only audit event `BILLING_BANK_CHARGE_UPDATED` recording old and new
  minor-unit amounts, without secrets.

Network TTL policy controls use `network.write` permission and a distinct audit
event `HOTSPOT_TETHERING_POLICY_UPDATED`.

## API and data interfaces

### Billing settings

```text
GET  /api/v1/billing/settings
PUT  /api/v1/billing/settings
```

Response:

```json
{
  "data": {
    "fixed_bank_charge_minor": 1500,
    "currency": "NGN",
    "updated_at": "2026-09-19T00:00:00Z"
  }
}
```

Writes accept a decimal NGN input at the HTTP boundary, parse it exactly to
minor units, require a step-up credential, and never use floats.

### Devices and subscriptions

```text
GET  /api/v1/portal/devices
POST /api/v1/portal/devices
GET  /api/v1/portal/account
POST /api/v1/payments
```

The payment request changes to include `device_id`:

```json
{
  "plan_id": "uuid",
  "device_id": "uuid"
}
```

The API ignores any client-provided amount, bank charge, subscription owner,
or MAC. The server verifies the authenticated customer owns the device.

Portal catalog responses include both the plan price and effective bank charge:

```json
{
  "price_minor": 50000,
  "bank_charge_minor": 1500,
  "total_minor": 51500,
  "currency": "NGN"
}
```

### Router policy

The router template/configuration will define a NetCore-owned tethering rule
set with stable comments and no hard-coded shared secret. It exposes a minimal
tenant configuration projection:

```text
enabled: boolean
expected_client_ttl: integer
```

Actual RouterOS application remains an operator-approved action. The platform
must render, validate, and audit the desired script; it must not silently modify
a router through the public portal.

## Failure handling

- Invalid device ID, a device belonging to another user, invalid MAC, retired
  plan, inactive tenant, or unavailable payment gateway returns a safe error
  and creates no payment/subscription.
- If gateway initialization fails, the frozen payment stays safely pending only
  under the existing retry/idempotency rules; it must not activate a plan.
- If payment verification reports the plan subtotal instead of the frozen
  customer total, verification fails and no subscription activates.
- If a RADIUS client connects with a MAC that has no subscription binding, it
  receives Access-Reject even if the account owns an active plan for another
  device.
- If tethering controls appear to block a valid direct client, an authorized
  operator can disable only the named NetCore tethering policy and investigate
  rule counters; no broad firewall reset is used.

## Testing and rollout

### Automated tests

- Unit tests for exact NGN decimal-to-minor conversion and no floating point
  rounding.
- Payment initiation tests for zero, N15, and changed future bank charges;
  idempotent retries retain the original values.
- Gateway verification tests reject mismatch between N500 service price and
  N515 customer total.
- Tenant/RLS tests prove users cannot buy plans for other users' devices.
- RADIUS SQL tests prove a MAC can use only its bound subscription, including
  when one account has multiple active plans.
- Router template tests assert that rules are idempotent, scoped, and include
  rollback identifiers.
- HTTP/portal tests assert price breakdown presentation and device selection.

### Production rollout order

1. Deploy database migration and API/UI changes with the charge defaulting to
   zero and tethering policy disabled.
2. Configure the Data Hub bank charge to N15.00 and verify a checkout displays
   N500 + N15 = N515 before making a real payment.
3. Test a second device purchase under one existing account and verify both
   subscriptions appear separately.
4. Apply the rendered TTL policy to a single test HotSpot/router scope.
5. Test direct browsing, Android tethered browsing denial, payment walled
   garden, portal access, DNS, and logout.
6. Enable the policy for the full HotSpot only after the test matrix passes.

## Open operational prerequisites

- The existing RADIUS portal acceptance test must pass before adding TTL rules;
  anti-tethering must not hide an unresolved access authorization problem.
- Tenant Resend delivery must be fixed before accepting the full new-customer
  sign-up journey.
- The administrator must decide the initial customer-facing copy for a blocked
  tethering attempt and whether it links back to the portal.
