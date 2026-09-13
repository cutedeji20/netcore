# Customer Registration Policy Design

## Purpose

NetCore will support tenant-controlled customer registration verification while keeping public registration tenant-bound and resistant to account enumeration. A customer must provide an email address, an E.164 phone number, and a password. Email verification and phone verification are independent tenant settings, both disabled by default.

Phone verification cannot be enabled until an SMS delivery provider is implemented and configured. Email verification can be enabled or disabled immediately through an authenticated, step-up-protected administrator workflow.

## Password Policy

New customer registrations and customer password resets accept passwords from 4 through 12 characters inclusive. A password must contain at least one letter and at least one decimal digit. Symbols are permitted. The password confirmation is enforced in the portal, while the API independently enforces the length and composition rules.

Existing password hashes remain valid regardless of their original password length. The new policy applies only when a customer registers or resets a password. Staff bootstrap, invitation, and privileged authentication password policies do not change.

## Phone Identity

Phone is mandatory for public customer registration. It must be a canonical E.164 value: a leading `+` followed by 7 through 15 decimal digits. The portal will show an example such as `+2348031234567`, but the API is authoritative.

The canonical phone value is stored on both the `users` identity and its linked `customers` profile. The existing tenant-scoped partial unique index on `users (tenant_id, phone)` prevents one phone identity from being assigned to multiple users. Public responses must not reveal whether an email address or phone number already exists.

## Persistence

Migration `0041_customer_registration_policy` adds these columns:

- `tenants.email_verification_required BOOLEAN NOT NULL DEFAULT FALSE`
- `tenants.phone_verification_required BOOLEAN NOT NULL DEFAULT FALSE`
- `tenants.registration_policy_version BIGINT NOT NULL DEFAULT 1`
- `users.registration_completed_at TIMESTAMPTZ`

The migration backfills `registration_completed_at` for existing users whose `email_verified_at` is non-null. Staff accounts continue to use the existing privileged-MFA authentication path, so this customer-completion field does not weaken or replace staff controls.

`email_verified_at` and `phone_verified_at` record actual verification only. They are not populated when verification is disabled. `registration_completed_at` is the durable customer-login gate and records that every verification step required by the tenant at registration time was satisfied or not required.

The down migration removes only the four columns introduced above.

## Tenant Registration Policy

The workspace snapshot adds:

- `email_verification_required`
- `phone_verification_required`
- `registration_policy_version`

`registration_policy_version` is a monotonically increasing value used for optimistic concurrency. A policy update supplies the version last read by the administrator. An update with a stale version is rejected with `409 REGISTRATION_POLICY_CHANGED`.

The authenticated endpoint is:

```text
PATCH /api/v1/workspace/registration-policy
```

Request body:

```json
{
  "email_verification_required": true,
  "phone_verification_required": false,
  "version": 3,
  "password": "current administrator password",
  "mfa_code": "123456"
}
```

The endpoint requires an authenticated principal with `workspace.write`, then re-verifies the administrator's current password and six-digit TOTP code. The store updates only the principal's tenant and writes an audit record containing the old and new Boolean values and the resulting version. Passwords and MFA codes never enter audit metadata or logs.

Email verification may be enabled or disabled. Enabling phone verification before an SMS notifier is configured returns `409 SMS_PROVIDER_REQUIRED` and does not modify the policy. Disabling phone verification remains available. Both settings default to `false` for existing and new tenants.

## Public Registration API

`POST /portal/auth/register` accepts:

```json
{
  "email": "customer@example.com",
  "phone": "+2348031234567",
  "password": "wifi7"
}
```

The tenant remains deployment-bound; the request cannot select a tenant. Rate limiting binds to the normalized email, phone, and client IP without storing raw identifiers in Redis keys.

The account service loads the active tenant and its registration policy server-side. The public request cannot select or override verification behavior.

### No verification required

When both settings are disabled, one tenant-scoped transaction:

1. creates a new identity or safely refreshes an incomplete identity;
2. stores the normalized email, canonical phone, and Argon2id password hash;
3. links a matching unlinked customer profile or creates a default profile containing email and phone;
4. sets `registration_completed_at`;
5. writes `CUSTOMER_REGISTERED_WITHOUT_VERIFICATION` without sensitive metadata.

The transaction never changes a completed or verified identity through public re-registration.

The response is:

```text
201 Created
```

```json
{
  "verification_required": "none"
}
```

### Email verification required

When email verification is enabled and phone verification is disabled, registration prepares an incomplete identity and issues the existing recipient-bound email OTP. The response is:

```text
202 Accepted
```

```json
{
  "verification_required": "email",
  "challenge_id": "opaque challenge identifier",
  "expires_at": "RFC3339 timestamp"
}
```

Successful `POST /portal/auth/verify-email` sets `email_verified_at` and `registration_completed_at`, links or creates the customer profile with both email and phone, and writes the existing email-verification audit event. OTP failure remains generic and does not partially complete registration.

Phone verification cannot be active in this release because the policy endpoint rejects enabling it without an SMS provider. The policy and completion model intentionally permit a later SMS challenge to delay `registration_completed_at` until all enabled steps are complete.

## Login and Password Recovery

Customer login requires `registration_completed_at` instead of inferring completion from `email_verified_at`. Staff users that require privileged MFA retain their existing login path and are not governed by public customer registration policy.

Password-reset requests remain non-enumerating. A successful email reset challenge may update the password only for a completed customer account. If `email_verified_at` is absent, successful consumption of the recipient-bound email reset code also sets it because the reset itself proves control of the address. Password reset uses the same 4–12 character, letter-and-digit policy as registration.

## Portal User Experience

The registration form adds a required Phone number field with E.164 guidance. Password and confirmation inputs use `minlength="4"` and `maxlength="12"`. JavaScript validates email presence, canonical phone syntax, password length and composition, and matching confirmation before submission; server validation remains authoritative.

The portal follows `verification_required` from the API:

- `none`: move to sign-in and display `Account created. Sign in to continue.`
- `email`: retain the password and phone only in page memory, show the email-code view, then move to sign-in after verification.

The portal does not fetch or infer tenant verification settings before registration. This avoids stale client policy and keeps the server authoritative.

Duplicate or completed identity cases use generic messages that do not disclose whether the email or phone is registered.

## Administrator User Experience

Workspace Settings adds a Customer registration section with Email verification and Phone verification controls. Current values come from `GET /api/v1/workspace/settings`.

Changing a control opens a step-up dialog requiring the administrator's current password and six-digit authenticator code. Email verification can be saved. Phone verification is visibly unavailable until an SMS provider is configured; a forced or stale client request is still rejected server-side.

The UI refreshes the workspace snapshot after a successful update. A stale-version response prompts the administrator to review the newly loaded values instead of overwriting another administrator's change.

## Errors and Security Boundaries

- Invalid email, phone, or password returns `400 INVALID_REQUEST` with field-safe guidance.
- Stale policy updates return `409 REGISTRATION_POLICY_CHANGED`.
- Enabling unavailable phone verification returns `409 SMS_PROVIDER_REQUIRED`.
- Failed step-up returns the existing generic authorization response.
- Store or notifier failures return service-unavailable responses without exposing database, provider, or account state.
- Tenant ID and policy are resolved from trusted server context.
- Public registration cannot assign roles or privileges.
- Existing verified/completed identities cannot be overwritten through registration.
- Raw passwords, OTPs, phone numbers, and emails are excluded from application logs and audit metadata.
- Existing rate limiting and Argon2id hashing remain in force. The interface will not describe the four-character minimum as secure.

## Verification Strategy

Automated verification covers:

- migration defaults, backfill, RLS compatibility, policy versioning, and rollback;
- Go and JavaScript password boundaries plus letter/digit composition;
- mandatory E.164 phone normalization and uniqueness conflicts;
- no-verification and email-verification registration responses and state transitions;
- completed-account login behavior and unchanged privileged-MFA behavior;
- password reset for completed accounts and email proof on successful reset;
- `workspace.write`, step-up, tenant isolation, optimistic concurrency, audit metadata, and unavailable-SMS behavior;
- portal DOM, request payload, and navigation behavior;
- workspace policy controls and error states;
- full Go and Node test suites plus migration/configuration checks.

Production acceptance must test both email-policy states against the deployed API and portal. Phone verification remains disabled until a separately reviewed SMS integration is available.
