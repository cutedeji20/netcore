# Access and customer foundation

**Status:** approved for planning
**Date:** 2026-08-24
**Scope:** release 1 of the dependency-first operations programme

## Purpose

Make the NetCore control dashboard safe and useful for daily operations before
adding subscription, voucher, router, and billing mutations. This release gives
authorised staff a secure way to manage their team and customer records, and
makes every dashboard view report live-data, empty, and failure states reliably.

It does not add subscription provisioning, voucher issuance, router/AAA writes,
or new Paystack flows. Those are separate follow-up releases.

## Existing foundations

The application already has tenant-scoped authentication, MFA, role permissions,
auditing patterns, eight-hour server-enforced sessions, Resend configuration,
and read APIs for the operational dashboard. The Plans page is the reference
mutation pattern. Most other dashboard pages are read-only today.

## Staff account model

### Built-in roles

The first release uses fixed, tenant-scoped roles. There is no custom-role
editor.

| Role | Allowed areas |
| --- | --- |
| Administrator | All areas, including team, workspace, security, and Paystack/Resend credentials. |
| Operations | Customers, plans, subscriptions, vouchers, routers/AAA, and sessions. |
| Billing | Customers, subscriptions, invoices, payment records, and vouchers. |
| Support | Customer profile changes and customer/subscription/session lookup only. |

The API, not the browser, is the authority for every permission decision. The
UI hides unavailable actions, but a direct request from an unauthorised account
must return a permission error and make no change.

### Invitation lifecycle

1. An Administrator enters an email address and chooses one of the fixed roles.
2. The service verifies that Resend is configured and usable for the tenant.
3. The service creates a pending invitation with a randomly generated,
   single-use token. Only a cryptographic hash of the token is stored.
4. Resend delivers a URL containing the raw token. The URL expires after
   24 hours.
5. The recipient opens the URL, chooses a password, and completes MFA
   enrolment. Only then is the staff account activated.
6. The invitation becomes redeemed and cannot be reused.

Administrators never set, view, or reset another person’s password or MFA
secret. A failed email delivery leaves no active staff account. The administrator
sees a delivery failure and can retry after correcting the mail configuration.

Resending an invitation invalidates all earlier links. An administrator can also
revoke an unredeemed invitation.

### Staff lifecycle and session safety

- Existing staff may have their role changed by an Administrator.
- A role change, account deactivation, or security revocation invalidates all
  sessions for that staff account immediately. A new login and MFA check are
  required before access continues.
- Staff accounts are deactivated rather than deleted, so access and audit
  history remain intact.
- The final active Administrator cannot be deactivated or demoted. A role
  change which would leave no active Administrator is rejected.
- Dashboard sessions retain the existing maximum lifetime of eight hours. The
  API is the source of truth; client-side expiry is only a usability aid.

## Customer model

### Customer records

Authorised Operations, Billing, Support, and Administrator staff can create,
edit, search, and deactivate tenant-scoped customer profiles. The profile
contains only service/contact information such as name, canonical email address,
and phone number. It does not create a customer portal password, portal session,
or staff role.

Customer email addresses are compared in their canonical form within a tenant.
Creating or changing a profile to a duplicate email is rejected with a clear
link to the existing customer record. This prevents split billing, subscription,
and payment histories.

Deactivation preserves the record and its audit history. It is not a destructive
delete. Future subscription and network releases will determine the connected
service action explicitly; this release does not silently terminate services.

### Portal identity link

Customers create their own email/password portal account during the captive
portal purchase journey. When that workflow confirms ownership of the same
canonical email address, it links to the pre-existing profile instead of
creating a duplicate customer. Staff cannot create portal credentials on the
customer’s behalf.

## Data and API boundaries

The release introduces narrowly scoped stores and services rather than adding
workflow logic directly to HTTP handlers.

| Component | Responsibility |
| --- | --- |
| Team service | Invite, resend, revoke, activate, role change, and deactivate staff while enforcing the final-administrator invariant. |
| Invitation store | Holds tenant, email, requested role, token digest, expiry, status, delivery metadata, and audit references. |
| Customer service | Validates and mutates customer profiles, canonicalises email, and enforces duplicate protection. |
| Audit writer | Records actor, tenant, target, action, result, and the approved before/after change summary. |
| Authentication service | Rejects expired sessions and sessions invalidated by a role change or deactivation. |
| Dashboard adapters | Load data when the application reports the active page and render loading, empty, error, and success states. |

New mutation endpoints are tenant-scoped, use existing CSRF/session protections,
require the corresponding server-side `*.write` permission, and return typed
validation and conflict errors. Secrets, password hashes, MFA secrets, raw
invitation tokens, and provider credentials never appear in an API response,
audit payload, or browser state.

## Dashboard behaviour

The Team page gains real actions for invitations and account lifecycle
management. The Customers page gains create/edit/deactivate actions and clear
duplicate messaging. Both use the Plans page’s mutation feedback as a reference
without copying its business logic.

All dashboard data adapters must use the application’s active-page render event,
not only a URL fragment check. Each page must visibly show one of these states:

- loading;
- populated live records;
- an intentional empty state with the next action; or
- a human-readable error with a retry path.

This removes the current failure mode in which a page can appear static or
blank after navigation even though its API is available.

## Failure handling

- Missing or invalid Resend configuration blocks delivery of a new invitation
  and provides a configuration/retry message. It never activates an account.
- Expired, redeemed, revoked, malformed, or tenant-mismatched invitation tokens
  are rejected without revealing account details.
- Concurrent invite, role, and deactivation operations use transactional
  checks to preserve one active administrator and one valid current invitation
  state.
- Validation, permission, duplicate, and unavailable-provider errors use stable
  client-safe messages and suitable HTTP status codes. Internal errors are
  logged with request context but no secret material.
- A dashboard action updates the live table only after server confirmation;
  failure leaves the last verified view intact.

## Audit requirements

Successful and rejected high-risk staff mutations are auditable. Required
events include invitation creation, resend, revoke, acceptance, role change,
deactivation, reactivation if later added, customer create/edit/deactivate, and
permission denial. Audit events include tenant ID, acting user ID, target ID
where applicable, timestamp, action, outcome, and a redacted change summary.

No raw password, MFA secret, session token, invitation token, integration
credential, or full payment secret is stored in audit records.

## Verification

Implementation is test-first. The test suite must cover:

- role permission allow/deny behaviour for every mutation endpoint;
- invite issuance, expiry, resend invalidation, revocation, single use, and
  activation only after password and MFA enrolment;
- immediate session rejection after staff role change or deactivation;
- final-active-administrator protection;
- customer canonical-email uniqueness, update conflicts, deactivation, and
  portal-profile linking;
- tenant isolation, CSRF/session requirements, redaction, and audit events;
- Resend unavailable/delivery failure handling;
- dashboard active-page navigation, loading/empty/error rendering, and
  successful mutation refreshes.

All focused tests and the complete Go/UI test suites must pass before the
release is committed or deployed. Production rollout will run the relevant
database migration first, recreate API/worker/UI, check health, and exercise a
non-destructive administrator and customer smoke test.

## Follow-up releases

This document intentionally does not specify the implementation of later
operational releases:

1. subscriptions and vouchers;
2. network and AAA;
3. revenue and automations.

Each gets its own approved design, plan, implementation, and deployment review.
