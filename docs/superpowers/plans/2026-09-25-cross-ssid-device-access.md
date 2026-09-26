# Cross-SSID Device Access Implementation Plan

> **For agentic workers:** Implement task-by-task with a failing test before each behavior change. Review each task before moving to the next.

**Goal:** Preserve one active subscription across APs and explicitly replace its one bound MAC across different SSIDs, with a same-customer admin recovery action and readable admin MAC labels.

**Architecture:** Keep `subscriptions.device_id` as the sole entitlement binding. Normal roaming uses the existing RADIUS path when the MAC is unchanged. Customer replacement is a short-lived pending request that activates only when a registered NAS presents the new MAC to RADIUS; admin replacement is a separate MFA-confirmed, audited transaction. Both preserve subscription and quota rows.

**Tech Stack:** Go API, PostgreSQL/RLS migrations and FreeRADIUS SQL, vanilla JavaScript UI, Node UI tests, Go tests.

**Spec:** [Cross-SSID device access design](../specs/2026-09-25-cross-ssid-device-access-design.md)

## Global constraints

- Only one device MAC is authorized per subscription at a time. Never infer physical-device equality from MACs.
- Never create a second subscription/payment, reset usage counters, or extend expiry during transfer.
- Target devices must be active and owned by the same tenant and customer; cross-customer transfers are out of scope.
- A live old-device network session blocks both transfer paths until confirmed closed.
- Fail closed on expired plan, exhausted quota, stale challenge, unregistered NAS, concurrent transfer, or database/RADIUS error.
- Preserve audit and subscription-event history. Do not write full MACs, codes, or credentials to ordinary logs.
- Keep replacement feature disabled by default until a controlled production pilot.

## Review focus

- Two simultaneous transfer requests for one subscription must not authorize both MACs (Tasks 2 and 4).
- A browser-forged portal MAC must not complete replacement without matching RADIUS `Calling-Station-Id` (Task 4).
- A staff target from another tenant/customer must fail even if its device ID is valid (Task 2).
- Old HotSpot sessions must not remain usable after transfer; while disconnect is not implemented, an open session must block transfer (Tasks 2 and 4).
- A quota-exhausted or expiring subscription must not gain new time or bytes during transfer (Tasks 2 and 4).

---

### Task 1: Show exact device identity to staff

**Files:** Modify `internal/subscriptions/subscriptions.go`, `internal/subscriptions/store_postgres.go`, `internal/subscriptions/http.go`, `cmd/ui/assets/live-customers.js`, and `cmd/ui/assets/live-subscriptions.js`. Test `internal/subscriptions/http_test.go`, `cmd/ui/assets/live-customers.test.js`, and a focused `cmd/ui/assets/live-subscriptions.test.js`.

**Interface:** Subscription read response gains `device: {id, label, normalized_mac}` or `null`; display helper formats a valid normalized MAC as `AA:BB:CC:DD:EE:FF` without changing the stored value.

- [ ] Add failing API/UI tests for labeled and unlabeled devices, legacy null binding, and read-only display. Run `go test ./internal/subscriptions` and `node --test cmd/ui/assets/live-customers.test.js cmd/ui/assets/live-subscriptions.test.js` to observe failures.
- [ ] Join the bound device in the subscription read query; allowlist only the three specified fields. Update the Customers grant selector and Subscriptions listing/details to show full colon-formatted MACs.
- [ ] Re-run the focused tests and `git diff --check`; inspect browser rendering at narrow and desktop widths. Commit this isolated UI/read-model change.

### Task 2: Add admin same-customer transfer

**Files:** Modify `internal/subscriptions/subscriptions.go`, `internal/subscriptions/http.go`, `cmd/api/main.go`, `cmd/ui/assets/live-subscriptions.js`; create `internal/subscriptions/transfer_store_postgres.go`; test `internal/subscriptions/http_test.go` and a focused transfer-store integration test.

**Interface:** `POST /api/v1/subscriptions/{id}/transfer-device` accepts `{target_device_id, reason, password, mfa_code}`. A distinct `TransferStore.Transfer(ctx, tenantID, actor, subscriptionID, targetDeviceID, reason)` performs the transaction. The HTTP/service layer requires `subscription.write`, same-origin protection, and `auth.Service.VerifyStepUp` before invoking it.

- [ ] Add failing HTTP tests for permission, malformed input, rejected step-up, and sanitized errors. Add database tests for same-customer transfer, foreign/inactive target, expired/not-active subscription, open session, pending customer transfer, duplicate/concurrent request, preserved quota/expiry/payment, and append-only audit/event rows.
- [ ] Implement the tenant-scoped locked transfer. Reject live old-device sessions rather than silently claiming to disconnect them. Reuse the existing staff device-registration endpoint for an unregistered target, exposed as an explicit action—not as a side effect of transfer.
- [ ] Add the admin confirmation UI showing old/new MAC, expiry, remaining quota, reason, password, and MFA code. Verify server revalidation rather than trusting displayed values.
- [ ] Run `go test ./internal/subscriptions ./internal/auth ./internal/devices` and targeted UI tests; commit.

### Task 3: Correct ordinary portal handoff selection

**Files:** Modify `internal/portal/store_postgres.go`, `internal/portal/http.go`, `internal/portal/portal.go`; test `internal/portal/portal_test.go`, `internal/portal/http_test.go`, and a database-backed portal-store test.

**Interface:** Normal `IssueHandoff` selects only an active, unexpired subscription whose active bound device matches the connecting normalized MAC. Use a distinct error for active plans that are bound to another device so the portal can offer replacement rather than claiming no plan exists.

- [ ] Add failing tests for two active subscriptions with different devices, bound-device mismatch, expired/exhausted plan, foreign device, and no eligible plan.
- [ ] Replace the earliest-active-plan query with customer/tenant/device-matched selection; keep NAS validation and nonce handling unchanged. Distinguish mismatch from no active subscription without exposing other customers' devices.
- [ ] Run `go test ./internal/portal` and relevant SQL policy tests; commit.

### Task 4: Customer-verified pending replacement and RADIUS activation

**Files:** Create the next sequential `db/migrations/0055_*` up/down pair and focused `internal/portal` replacement store/service files; modify `internal/portal/http.go`, `internal/portal/store_postgres.go`, `db/migrations/0051_radius_device_bound_subscriptions.up.sql` successor function via migration (do not edit applied migrations), `cmd/api/main.go`, and focused portal/RADIUS tests. Reuse the existing email delivery and verification patterns where suitable.

**Interface:** Authenticated portal endpoints issue and verify a short-lived, single-use email challenge for one named subscription and target MAC/NAS. Verification creates one pending replacement. A new versioned RADIUS function recognizes only that pending request when nonce, registered NAS, observed `Calling-Station-Id`, target ownership, expiry, quota, and old-session closure all match; in one transaction it swaps `device_id`, consumes the pending request, and appends audit/event rows before returning access policy.

- [ ] Write failing migration/service tests for challenge replay/expiry, forged browser MAC, wrong NAS, foreign target, concurrent pending requests, old session, expired/exhausted plan, and unchanged subscription/usage/payment fields.
- [ ] Add the tenant-scoped pending table, unique live-request constraint, RLS/grants, and forward-only replacement of the RADIUS function in `0055`. Ensure down migration restores the prior function and drops only new objects.
- [ ] Implement challenge delivery/verification with rate limits and no raw code persistence. Do not change binding when issuing a browser-side request.
- [ ] Implement atomic activation at RADIUS observation. Verify old-MAC MAC-auto-login and handoff reject after transfer, and new-MAC authorization succeeds. Run migration up/down/up and targeted Go/SQL tests; commit.

### Task 5: Portal UI, rollout guard, and acceptance

**Files:** Modify `cmd/ui/assets/portal.js`, `cmd/ui/assets/portal.html`, `cmd/ui/assets/portal-account.js`, associated UI tests, production config/docs, and deployment config tests.

**Interface:** A disabled-by-default server flag controls replacement endpoints and UI capability. The portal shows current/bound colon-formatted MAC, exact plan, expiry, and remaining quota; it offers replacement with verified email challenge and clear old-device warning only for eligible plans.

- [ ] Add failing UI/config tests for matching MAC (normal login), different MAC (offer transfer), missing/invalid context, active old session, code failure, and disabled flag.
- [ ] Implement UI and feature guard. Preserve ordinary checkout and account flows.
- [ ] Run Go tests, Node UI tests, migration tests, and production config tests; review security boundaries and `git diff --check`. Commit.
- [ ] In staging, test shared-SSID roam and different-SSID replacement with a real bridged AP, MikroTik DHCP/HotSpot, and FreeRADIUS. In production, obtain backup and rollout approval, deploy disabled, pilot MAAL, compare before/after subscription ID, MAC, expiry and usage, then enable broadly only after acceptance. Do not claim production success from source tests alone.
