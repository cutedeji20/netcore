# Reseller and Bearer Voucher Access Design

## Goal

Let an administrator issue prepaid voucher codes on demand, let registered
customers buy one or multiple codes, and let any bearer use a code for internet
access without creating an email/password account. A purchaser and a bearer may
be different people. Existing customer sign-in and direct plan purchase remain
available. A tenant can designate a user or customer as a reseller and set one
reseller discount for plan and voucher purchases.

"Coupon" and "voucher" name the same prepaid access code throughout the UI
and API. Use "voucher" in new interfaces.

## Current boundaries

- Portal login uses `/portal/auth/login` and a customer session. The existing
  handoff endpoint requires that session.
- Every subscription and device references a customer. A customer may have no
  linked login user; this is the basis for a guest entitlement record.
- Voucher rows already hold tenant, hashed code, plan, status, expiry, optional
  reseller, and redemption fields. The exposed voucher API only lists staff-safe
  batch inventory; it does not issue, sell, or redeem codes.
- A direct purchase freezes plan price and the fixed bank charge in a pending
  payment. Billing settings currently hold the fixed NGN bank charge.
- RADIUS enforces subscription expiry and plan quota through the existing
  device and handoff path.

## Identity and ownership

A voucher buyer must be a verified, signed-in customer. The buyer owns the
purchase record and receives the codes. Buying a voucher grants no entitlement
to the buyer's device. A reseller is a designation on the existing tenant user
identity, not a replacement for existing staff roles or a new login method.
Only an administrator can add or remove that designation. The operation uses
password and MFA step-up and writes an immutable audit record.
Any tenant user may be designated, but checkout requires a verified email and
a linked customer profile. Promotion creates the customer profile if absent;
a user without a verified email can hold the designation but cannot purchase
until email verification is complete.

The bearer need not register or prove ownership of the buyer account. First
successful use creates an internal guest customer with no linked user, a device
record for the observed MAC, and a voucher-backed subscription. Keep the buyer,
issuer, and guest bearer as distinct references. Guest records must be marked
as such in the schema and excluded from ordinary registered-customer account
flows. If the MAC already belongs to another customer, fail without taking
over that device; an authenticated account flow or staff resolution can handle
that conflict later.

The voucher is a bearer secret. First use binds it to one device and activates
its subscription. Re-entering the same code for that device may create a new
short-lived handoff while the subscription remains active. Another device
cannot use it. Expiry or quota exhaustion denies further handoffs and RADIUS
access. Staff revocation stops future access through the normal entitlement
state transition. The public UI must not call this an email/password login.

## Pricing and payments

Store one tenant-wide reseller discount in basis points, from 0 to 9,999.
Calculate the discount on each plan's current minor-unit price using integer
arithmetic, rounding the discount down to a whole minor unit. Add the fixed
bank charge after the discount. Direct plan checkout and voucher checkout
resolve reseller status and settings on the server; the browser submits plan
and quantity, never a payable amount or discount. A voucher order quantity is
1 to 100. Each voucher has one unit of the selected plan; an order pays for
the quantity, with one fixed bank charge per order.

At initiation, freeze base unit price, quantity, discount, bank charge, final
amount, currency, and reseller status in the pending order/payment record.
Gateway verification must match that frozen amount. Settings changes, role
changes, and plan price changes affect only later initiations. Historical
payments and invoices are never recalculated. Voucher issuance is triggered
only after verified payment and is idempotent under duplicate callbacks and
customer retries. A successful voucher purchase must not also activate a
subscription for the buyer. Use a voucher order record linked to a payment with
nullable `subscription_id`; the existing direct-plan payment path stays
separate. The order-to-payment link and per-order issuance uniqueness prevent
one payment from producing more than the purchased quantity.

## Voucher issuance and delivery

An administrator can issue 1 to 100 codes per operation without payment,
selecting the plan, code expiry, and a reason. The operation requires the
appropriate administrator permission and step-up, and records actor, plan,
quantity, reason, and issued voucher IDs. Customer and reseller orders issue
exactly the paid quantity after payment success and retain a link to the
purchase. A failed or pending payment issues none.

Generate high-entropy unpredictable codes. Store a code hash for redemption,
a short non-secret support prefix, and separately encrypted code material for
delivery to its issuer or buyer. Use a dedicated versioned encryption key and
the existing envelope-encryption boundary; never store plaintext. Only the
authenticated buyer or issuing administrator may retrieve their codes, with
step-up, rate limits, and an audit record for every reveal or download. This
lets a buyer recover codes for resale without issuing replacements. Never put
raw codes in logs, audit metadata, URLs, analytics, or ordinary list responses.
Revocation stops access but does not erase purchase or issuance history.
Refunds and automatic replacement of sold codes are outside this release.

Issuing a voucher fixes its plan duration, speed, device/session limits, and
quota terms. The narrow first release keeps those non-price terms locked on a
plan while any unredeemed or active voucher references it. Plan price may
change for future sales. Voucher expiry before use is distinct from the
subscription expiry that starts on first redemption.

## Guest redemption and network handoff

The captive portal offers "Use a voucher" beside email/password sign-in.
The guest request supplies the code and router-provided connection context.
The server derives the tenant from deployment configuration, validates the
origin, normalizes the MAC and NAS, applies account-independent IP/device/code
attempt limits, and never reveals whether a code belongs to another tenant.
The browser's context is a binding candidate; the final RADIUS consume check
must still match the actual NAS and MAC.

Under one tenant-scoped transaction, lock the voucher, reject unavailable
codes, create the guest customer/device/subscription and usage period on first
use, and transition the voucher to redeemed. Concurrent first-use attempts
must yield one entitlement. A later same-device request reads that entitlement
without restarting duration or quota. Create the same short-lived, one-time
handoff used by the current portal path, then let RouterOS and FreeRADIUS
authorize access. Guest subscriptions have an explicit voucher funding state;
they must not be counted as a new direct gateway payment on redemption.

## UI and permissions

- Customer portal: ordinary sign-in and plan purchase; voucher entry without
  sign-in; signed-in voucher purchase with single/bulk quantity, price
  breakdown, and authenticated code retrieval.
- Reseller portal: the same customer flows with server-calculated discounted
  pricing and purchased voucher inventory. Promotion or removal changes
  future checkout prices only.
- Staff dashboard: administrator designation and removal, reseller discount
  beside bank charge, on-demand code issuance, batch status, and revocation.
  Ordinary inventory remains free of raw codes and hashes.

## Verification and rollout

Use migrations for every schema and permission change, with tenant RLS and
rollback tests. Test price rounding and frozen amounts; role or setting changes
during pending checkout; duplicate callbacks; bulk quantity boundaries;
encrypted code retrieval; code guessing limits; cross-tenant isolation; simultaneous
redemption; same-device reconnect; other-device denial; registered-MAC
conflict; code revocation; expiry; quota exhaustion; and RADIUS MAC/NAS
matching. Confirm direct customer login and plan payment still work.

Release schema and server pricing first, then issuance and buyer delivery,
then guest redemption and UI behind a default-off feature flag. Pilot one
admin-issued voucher and one paid voucher on a controlled device. Verify the
subscription, quota, and RADIUS session, and prove another device cannot use
the code before broad enablement. Roll back by disabling guest redemption and
new sales; preserve issued codes, orders, payments, and audit history.
