# Cross-SSID device access and visible MACs

## Goal

An active, paid or staff-granted subscription must remain usable when its customer moves between transparently bridged APs. On a shared SSID the client MAC should remain stable and no account change is needed. On a different SSID, where a phone may present a different randomized MAC, the customer may explicitly replace the subscription's one authorized device without buying or granting another plan. An authorized admin may make the same one-device transfer between devices registered to that same customer. Staff must be able to read the bound MAC directly in the admin UI.

## Current evidence and boundaries

- On the wired EAP113 in AP mode, MAAL appears as `46:38:8D:DB:B0:F9` in the EAP client list, MikroTik DHCP lease, and MikroTik HotSpot host. The AP is preserving the client MAC in this tested path.
- MAAL's account has an active subscription that works on its original AP/SSID. Its registered-device list does not include `46388ddbb0f9`. A different SSID can yield a different randomized MAC on the same phone.
- `subscriptions.device_id` binds a plan to one device. `radius_portal_access` and `radius_device_mac_autologin` require the observed client MAC to match that device. The portal currently picks the earliest active subscription before matching the connecting MAC, so it can issue a handoff that RADIUS must reject.
- The RouterOS HotSpot and the RADIUS server cannot prove that two MACs belong to the same physical phone. The product therefore authorizes a verified *replacement*, not automatic MAC equivalence.
- No change to pricing, plan duration, quota ledger, payment history, or AP DHCP/NAT is part of this feature.

## Network prerequisite

Every customer AP must be a transparent layer-2 bridge into the intended MikroTik HotSpot network, with a single loop-free uplink and no AP-side DHCP or NAT. Test each AP by comparing one client's MAC in AP client list, DHCP lease, and HotSpot host. A shared SSID with the same security settings is preferred for seamless roaming. A different SSID may require the explicit replacement flow below. No application change can repair a repeater that translates customer MACs or a bridge loop.

## Admin presentation

The Customers > Grant access registered-device selector displays `Label · AA:BB:CC:DD:EE:FF` for every active device, even when a label exists. The subscription listing/details expose the subscription's bound device label and colon-formatted MAC separately from status and payment state. These fields are read-only; looking at them cannot grant or rebind access. Missing/legacy bindings display `Not assigned`, not a fabricated MAC. Normalized 12-hex MACs remain the storage and API comparison format; presentation formatting never changes identifiers used by RADIUS.

## Admin-assisted same-customer transfer

The subscription detail offers **Transfer to another registered device** only to staff with subscription-write permission. It lists active devices belonging to that subscription's customer, with full colon-formatted MACs; the current device is identified and is not a target. If a newly observed MAC is not yet registered, staff must first register it to the same customer through an explicit device-registration action after verifying the customer's identity and the MAC reported by the AP and MikroTik. The transfer form shows the subscription, current and target devices, original expiry, and remaining quota, and requires a reason plus fresh staff MFA step-up and a confirmation that the old device loses access. A staff member cannot select a device owned by another customer or tenant, enter an arbitrary unregistered MAC, or change subscription ownership.

The server rechecks all conditions in a tenant-scoped transaction: subscription status `ACTIVE`, unexpired period, existing device binding, active target device owned by the same customer, no open old-device session, and no pending customer replacement. It locks the subscription and device rows, changes only `subscriptions.device_id`, and appends a subscription event and staff audit record containing old/new device IDs, reason, and actor. It preserves the subscription ID, payment link/history, starts/expiry times, plan, and quota counters. It does not create a new subscription or payment. Duplicate requests and concurrent transfers fail safely; a transfer to the already-bound device is a no-op, not another audit event. Old-MAC RADIUS reauthentication must fail after commit. Because existing HotSpot authorization may outlive the DB change, an open old-device session blocks transfer until confirmed closed; the UI states this rather than claiming immediate disconnection.

This is a support recovery path, not an automatic response to a new SSID. It does not move a plan to another customer's account. A cross-customer entitlement transfer would require its own ownership, consent, accounting, refund, and audit design.

## Customer replacement flow

From the captive portal on the *new* SSID, the authenticated customer sees the current network MAC and the active subscription's bound MAC and may choose **Move this plan to this device**. The UI explains that the old MAC will stop being eligible and that remaining time and quota are retained. The operation is limited to a customer-owned, active, unexpired, device-bound subscription, an active target device, and a registered active NAS. It never selects another customer's or tenant's subscription.

The replacement requires a short-lived, single-use step-up challenge delivered to the account's verified email, independent of the normal portal session. The target MAC supplied by the browser remains untrusted until the MikroTik/FreeRADIUS handoff sees it as `Calling-Station-Id` on the registered NAS. The service records a pending replacement after step-up, but does not change `subscriptions.device_id` yet. The RADIUS handoff transaction locks the subscription and pending replacement, validates the observed MAC, target device ownership, challenge expiry, and session/concurrency rules, and atomically updates the bound `device_id`, consumes the pending replacement, and emits an audit/subscription event before granting access. A failed or expired handoff leaves the old binding unchanged. The normal handoff path must choose a subscription matching the observed MAC; a pending replacement uses its own explicit, validated path rather than the current earliest-active-plan selection.

Only one active/pending replacement per subscription is allowed. A replacement is denied while an old-MAC network session is open; the portal instructs the user to disconnect from the old SSID and retry after that session ends. This is deliberately fail-closed until a verified router-disconnect mechanism exists, because changing the database binding alone would not terminate an already authorized MikroTik session. After a successful replacement, old-MAC reauthentication and MAC auto-login must fail. Existing sessions, usage counters, expiry, payment records, and subscription ID are never reset or recreated. If the new device MAC is already registered to another customer, reject it without moving ownership.

The service rate-limits challenge issuance, code verification, and replacement attempts by account and target MAC. Responses do not expose whether another customer owns a MAC. The existing **Grant access** action must never silently rebind a subscription; admin-assisted transfer uses the separate permissioned, reasoned, MFA-confirmed flow above.

## Failure behavior

- No active eligible subscription: display a clear no-active-plan message; do not create or charge for a plan.
- Already bound to observed MAC: proceed with ordinary handoff; do not trigger replacement.
- Missing/invalid portal connection context, NAS, or observed MAC: deny without changing binding.
- Invalid/expired/reused step-up code or pending handoff: deny without changing binding.
- Old active session or concurrent replacement: deny with a retry instruction; no partial transfer.
- Admin target device not owned by the same customer, inactive, or unregistered: deny without a transfer.
- Staff permission or fresh MFA missing: deny without a transfer or audit success event.
- Quota exhausted or expired during replacement: deny; never reset quota or extend expiry.
- RADIUS/DB error: fail closed, preserve old binding, and log a correlation ID without secrets or full MAC in general logs.

## Verification and rollout

Write tests for matching-MAC selection, wrong-MAC denial, verified pending replacement, RADIUS-observed-MAC activation, replay/expiry, cross-tenant and cross-customer rejection, active-session denial, unchanged quota/expiry, and old-MAC denial after transfer. Test admin permission/MFA, same-customer target checks, missing/inactive devices, duplicate/concurrent requests, old-session denial, unchanged billing/usage/expiry, and complete audit history. Test the admin selector and subscription display with labeled, unlabeled, and legacy-unbound devices. Run migration and rollback checks against a test database before production.

Deploy server and UI together behind a disabled-by-default replacement feature flag. First verify same-SSID roaming without replacement, then pilot MAAL on a different SSID with before/after subscription ID, bound MAC, expiry, usage counter, RADIUS result, and HotSpot authorization. Enable broadly only after the pilot and a loop-free AP/MAC-preservation audit. Do not bulk-rebind existing subscriptions or edit production rows manually.
