# Reseller and Bearer Voucher Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let verified customers and resellers purchase prepaid vouchers, let administrators issue them, and let an unregistered bearer activate one device until time or quota expires.

**Architecture:** Keep buyer payments separate from bearer entitlements. Price and freeze purchases inside tenant transactions, issue encrypted voucher codes only after verified payment or administrator step-up, and redeem a code into a guest customer/device/subscription before issuing the existing RADIUS handoff. Keep all new sale and guest routes behind default-off flags until the pilot passes.

**Tech Stack:** Go 1.25, PostgreSQL migrations and forced tenant RLS, vanilla JavaScript, Docker Compose, Azure Key Vault envelope wrapping, RouterOS/FreeRADIUS.

**Spec:** `docs/superpowers/specs/2026-10-09-reseller-voucher-access-design.md`

## Global Constraints

- `coupon` and `voucher` are one code type; new UI and API use `voucher`.
- Reseller discount is 0–9,999 basis points; floor the minor-unit discount, then add one fixed bank charge per order.
- Voucher order quantity and administrator issuance quantity are each 1–100.
- Buyer, issuer, and bearer are distinct; buyer purchase creates no access subscription.
- First redemption binds one device. Same-device reconnect never resets time or quota.
- Preserve historical subscription, invoice, and payment amounts. A gateway-verified payment is the only paid issuance trigger.
- Store raw voucher codes only as encrypted envelopes and hashes; never log or place them in URLs or general list responses.
- Tenant predicates, forced RLS, immutable audit records, and final RADIUS MAC/NAS matching remain mandatory.
- New sale and guest routes default off in production until the controlled pilot.

## Review Focus

- A buyer loses reseller status after checkout: Task 3 asserts the pending payment amount does not change.
- A callback arrives twice: Task 5 asserts the order still owns exactly its paid quantity of vouchers.
- Two devices race to redeem one code: Task 7 asserts one binding and one subscription.
- A guest MAC already belongs to a registered customer: Task 7 asserts denial without moving that device.
- A code is revealed to a different tenant or buyer: Task 4 asserts a generic denial and no plaintext leak.

## File map

- `db/migrations/0059`–`0063`: reseller identity, discount settings, payment snapshots, voucher orders/code envelopes, and guest entitlement state, each with a down migration.
- `internal/billing`, `internal/team`: reseller setting and administrator designation.
- `internal/payments`: frozen direct-plan pricing, voucher checkout, gateway verification, and idempotent issuance trigger.
- `internal/vouchers`: generation, encryption, buyer inventory/reveal, redemption, and staff lifecycle.
- `internal/portal`: guest handoff while preserving existing account handoff.
- `internal/config`, `cmd/api/main.go`, `deployments/production/.env.example`: feature gates and route wiring.
- `cmd/ui/assets`: staff settings/roster/vouchers and customer portal screens.

---

### Task 1: Reseller identity and administrator designation

**Files:** Create `db/migrations/0059_reseller_policy.up.sql` and `.down.sql`; modify `internal/team/roles.go`, `internal/team/store_postgres.go`, `internal/team/http.go`; test in `internal/team/roles_test.go` and `internal/team/http_test.go`.

**Interfaces:** Produce `SetReseller(ctx context.Context, tenantID, userID string, enabled bool, actor MutationActor) error` and `IsReseller(ctx context.Context, tenantID, userID string) (bool, error)` in the team store. The mutation requires an Administrator principal and existing step-up verification; it preserves other roles and atomically ensures a linked customer profile. Unverified email blocks later purchase, not designation.

- [ ] Add tests: promotion/removal preserves staff roles, another tenant's user is rejected, missing verified email can be designated, and an audit event records actor and prior/new state. Run `go test ./internal/team -run Reseller -count=1`; expect red.
- [ ] Add the role/permission migration with tenant RLS and reversible permission grants; implement the store and `PUT /api/v1/users/{id}/reseller` route with password/MFA step-up.
- [ ] Run the focused tests and migration up/down check; expect green. Commit only Task 1 files.

### Task 2: Tenant reseller discount setting and exact pricing

**Files:** Create `db/migrations/0060_reseller_discount.up.sql` and `.down.sql`; modify `internal/billing/settings.go`, `settings_http.go`, `store_postgres.go`; create `internal/billing/reseller_pricing.go`; test in `internal/billing/settings_test.go` and `reseller_pricing_test.go`.

**Interfaces:** Produce `CalculateResellerPrice(baseMinor int64, discountBasisPoints int) (discountMinor, netMinor int64, err error)` using integer arithmetic. Extend settings with `reseller_discount_basis_points` and its own administrator step-up/audit update path; preserve the existing bank-charge value on a discount-only update.

- [ ] Add tests for 0 and 9,999 basis points, floor rounding, overflow/invalid input, tenant isolation, failed step-up, and old/new audit values. Run `go test ./internal/billing -run 'Reseller|Settings' -count=1`; expect red.
- [ ] Implement the settings schema, service, and `PUT /api/v1/billing/reseller-discount` with 0–9,999 validation and no client-supplied money total.
- [ ] Run focused tests and migration up/down; expect green. Commit Task 2 files.

### Task 3: Freeze discounted direct-plan checkout

**Files:** Modify `internal/payments/store_postgres.go`, `payments.go`, `http.go`; create `db/migrations/0061_payment_discount_snapshot.up.sql` and `.down.sql`; test in `internal/payments/payments_test.go` and `store_postgres_test.go`.

**Interfaces:** Extend the pending payment snapshot with base plan amount, discount basis points/amount, net plan amount, fixed bank charge, final amount, and reseller-at-initiation status. The database check must reconcile those parts. The existing `Service.Initiate` signature remains compatible; reseller status comes from the authenticated user in the tenant transaction. Add authenticated `GET /api/v1/payments/quote?plan_id=...` for display; checkout recalculates independently.

- [ ] Add tests showing normal checkout unchanged, reseller discount applied before bank charge, and unchanged pending/verified amounts after plan price, discount setting, or role changes. Run `go test ./internal/payments -run 'Initiate|Discount' -count=1`; expect red.
- [ ] Add the additive migration and server-side snapshot calculation; keep gateway verification against the frozen final amount and historical rows unchanged.
- [ ] Run payment tests plus migration up/down; expect green. Commit Task 3 files.

### Task 4: Voucher code security and authorized retrieval

**Files:** Create `internal/vouchers/code.go`, `envelope.go`, `issuance.go`, `issuance_store_postgres.go`, and focused tests; create `db/migrations/0062_voucher_orders.up.sql` and `.down.sql` with order, issuance, encrypted envelope, buyer/issuer, and unique order-position references; modify `internal/config/config.go` and `deployments/production/.env.example` for a dedicated versioned voucher KEK.

**Interfaces:** Produce `IssueBatch(ctx context.Context, tenantID string, request IssueRequest) (Batch, error)` and `RevealBatch(ctx context.Context, tenantID, actorUserID, batchID string) ([]IssuedCode, error)`. Reuse `integrations.KeyWrapper` with voucher-specific authenticated data bound to tenant and voucher ID. A dedicated Key Vault key ID must be configured before issuance/reveal can start.

- [ ] Add tests for unpredictable unique codes, hash-only redemption lookup, envelope round trip, wrong-tenant/wrong-voucher denial, unavailable key fail-closed, unauthorized buyer denial, reveal audit, and no raw code in ordinary inventory. Run `go test ./internal/vouchers -run 'Code|Issue|Reveal' -count=1`; expect red.
- [ ] Implement generation, encryption, tenant-scoped persistence, batch ownership, authorized reveal, and reversible schema. Never place codes in log, URL, audit metadata, or list JSON.
- [ ] Run focused tests and migration up/down; expect green. Commit Task 4 files.

### Task 5: Verified single/bulk voucher purchase

**Files:** Create `internal/payments/voucher_checkout.go`, `voucher_store_postgres.go`, tests; modify `internal/payments/webhook_store_postgres.go`, `http.go`, and `cmd/api/main.go`; use Task 4's `voucher_orders` schema linked to a payment with nullable `subscription_id`.

**Interfaces:** Add `POST /api/v1/voucher-orders` accepting plan ID and quantity 1–100 with an idempotency key, `GET /api/v1/voucher-orders/quote?plan_id=...&quantity=...` for display, and `GET /api/v1/voucher-orders/{id}` for the authenticated buyer. The order freezes quantity, unit base/net/discount, bank charge, and total. A successful gateway verification calls Task 4 issuance once for each order position; the buyer gains no subscription. Gate sale routes with a default-off flag from this task onward.

- [ ] Add tests for quantity 0/101 rejection, normal/reseller totals, unverified email denial, failed payment issuing zero codes, duplicate callback issuing exactly the paid quantity, and no buyer subscription. Run `go test ./internal/payments -run Voucher -count=1`; expect red.
- [ ] Implement tenant-bound order/checkout/verification paths using the existing gateway and payment idempotency boundary. Enforce order-position uniqueness in PostgreSQL.
- [ ] Run payment and voucher tests plus migration up/down; expect green. Commit Task 5 files.

### Task 6: Administrator issue/revoke and plan-term lock

**Files:** Modify `internal/vouchers/http.go`, `issuance.go`, `issuance_store_postgres.go`, `internal/plans/store_postgres.go`; add focused tests in `internal/vouchers/http_test.go` and `internal/plans/store_postgres_test.go`.

**Interfaces:** Add `POST /api/v1/vouchers/batches` for 1–100 administrator-issued codes with plan, pre-use expiry, and reason; add `POST /api/v1/vouchers/{id}/revoke`. Both require administrator permission and step-up. Preserve price-only plan changes while unredeemed vouchers exist; block non-price term changes until voucher history no longer depends on them.

- [ ] Add tests for missing reason/step-up, tenant isolation, audit, revocation, and plan duration/quota lock with price update allowed. Run `go test ./internal/vouchers ./internal/plans -run 'Issue|Revoke|Voucher' -count=1`; expect red.
- [ ] Implement the staff routes and plan guard; use Task 4 code generation and preserve voucher/order history on revocation.
- [ ] Run focused tests; expect green. Commit Task 6 files.

### Task 7: Atomic guest redemption and entitlement

**Files:** Create `db/migrations/0063_guest_voucher_entitlements.up.sql` and `.down.sql`; create `internal/vouchers/redemption.go`, `redemption_store_postgres.go` and tests; modify `internal/portal/store_postgres.go` and `internal/portal/portal.go`.

**Interfaces:** Produce `RedeemForDevice(ctx context.Context, tenantID, code, mac, nas string) (subscriptionID string, err error)`. First use locks the voucher, creates a marked guest customer, device, ACTIVE voucher-funded subscription and usage period, then binds the code. Later same-device use returns the same subscription without resetting time/quota. A registered-MAC conflict fails without moving ownership. Allow `portal_handoffs.user_id` to be NULL only for a voucher-funded guest subscription; retain the authenticated-user binding otherwise.

- [ ] Add database-backed tests for two-device race, same-device reconnect, other-device denial, duplicate guest/subscription prevention, pre-use expiry, revoked code, quota exhaustion, cross-tenant lookup, and registered-MAC conflict. Run `go test ./internal/vouchers ./internal/portal -run 'Redeem|Guest' -count=1`; expect red.
- [ ] Add guest marker/funding state and redemption transaction with forced RLS. Write subscription/usage/audit events through established boundaries, then enable the existing RADIUS policy to read the guest entitlement without changing its final MAC/NAS match.
- [ ] Run focused tests and migration up/down; expect green. Commit Task 7 files.

### Task 8: Public voucher handoff and feature gates

**Files:** Modify `internal/portal/http.go`, `internal/config/config.go`, `cmd/api/main.go`, `deployments/production/.env.example`; test in `internal/portal/http_test.go`, `internal/portal/deployment_templates_test.go`, and `internal/config/config_test.go`.

**Interfaces:** Add `POST /api/v1/portal/voucher-handoff` with code and captive connection context. Gate voucher sales and redemption separately, default false. Apply allowed-Origin checks and IP/device/code-hash attempt limits before Task 7 redemption. Add `Service.IssueGuest(ctx context.Context, tenantID, subscriptionID, mac, nas, hotspotLoginURL string) (Handoff, error)` with a distinct store path that verifies the guest subscription before inserting a handoff with NULL `user_id`. Return only the existing short-lived handoff token; never return code, MAC, NAS, or subscription ID.

- [ ] Add tests for flags-off 404, rate limits, invalid/foreign code generic errors, bad Origin, forged browser MAC/NAS, same-device reconnect, and final RADIUS binding. Run `go test ./internal/portal ./internal/config -run 'Voucher|Guest' -count=1`; expect red.
- [ ] Wire the public handler to Tasks 4/7 with server-derived tenant and existing RouterOS handoff construction. Keep account login unchanged.
- [ ] Run focused tests; expect green. Commit Task 8 files.

### Task 9: Customer, reseller, and staff UI

**Files:** Modify `cmd/ui/assets/portal.html`, `portal.js`, `portal.css`, `live-billing-settings.js`, `live-team.js`, `live-vouchers.js`; create `cmd/ui/assets/portal-vouchers.js` and focused `.test.js` files.

**Interfaces:** Portal offers email/password or voucher entry without an account, single/bulk order checkout for authenticated buyers, reseller breakdown from server quotes, and step-up protected code retrieval. Staff UI offers reseller designation, discount setting, administrator issuance, status, and revocation; general lists never render code material.

- [ ] Add Node tests for guest entry, quantity and totals, failed checkout/return, masked inventory, authenticated reveal, staff step-up, and no code in URL/storage/logging. Run `node --test cmd/ui/assets/*voucher*.test.js cmd/ui/assets/billing-settings.test.js`; expect red.
- [ ] Implement UI states with existing portal and dashboard patterns; use feature flags to hide unavailable actions, but rely on API authorization.
- [ ] Run focused Node tests and UI build/checks; expect green. Commit Task 9 files.

### Task 10: Integration, pilot, and rollout

**Files:** Modify `deployments/production/README.md` and focused integration/invariant tests under `tests/`; update production config only after acceptance.

**Interfaces:** Release schema/server first with both flags off, then issuance/buyer delivery, then one controlled guest pilot before enabling the public guest route. Rollback switches flags off and leaves financial/audit history intact.

- [ ] Run `go test ./...`, `go vet ./...`, focused Node tests, migration up/down and `tests/invariants.sql`; resolve any release-blocking failures or document an independent pre-existing failure with evidence.
- [ ] On staging, verify one admin voucher and one paid reseller voucher: purchase amount, encrypted retrieval, buyer/bearer separation, single guest subscription, expiry/quota, RADIUS MAC/NAS match, and other-device denial.
- [ ] On production, confirm backup and migration state, apply migrations, build and health-check API/UI/worker, run one non-critical controlled pilot, then enable sales and guest flags. Record revision, service state, and browser/device evidence; retain a flags-off rollback path.

## Execution notes

Tasks 1–3 can be reviewed as a pricing slice. Tasks 4–6 form issuance and sales. Tasks 7–9 form guest access and presentation. Never infer production acceptance from local tests or a healthy container alone.
