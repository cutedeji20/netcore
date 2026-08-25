-- Fixed staff access policy, invitation state, and encrypted MFA envelope metadata.
-- Ciphertext and Key Vault wrapping metadata are persisted; secret values are never
-- stored in this schema, audit data, application state, or logs.

BEGIN;

-- Preserve the existing first-administrator role where it is the only
-- canonical administrator for its tenant. If an Administrator already exists,
-- the historical Platform administrator row remains untouched.
UPDATE roles AS legacy
   SET name = 'Administrator'
 WHERE legacy.name = 'Platform administrator'
   AND NOT EXISTS (
       SELECT 1
         FROM roles AS canonical
        WHERE canonical.tenant_id = legacy.tenant_id
          AND canonical.name = 'Administrator'
   );

-- Every tenant receives exactly the canonical role names. Existing canonical
-- rows and their user assignments are retained.
INSERT INTO roles (tenant_id, name)
SELECT tenant.id, role.name
  FROM tenants AS tenant
 CROSS JOIN (VALUES ('Administrator'), ('Operations'), ('Billing'), ('Support')) AS role(name)
ON CONFLICT (tenant_id, name) DO NOTHING;

-- If both names existed, retain every user by moving the legacy membership to
-- the canonical role before retiring the legacy role and its grants. This
-- closes the duplicate-role privilege path without changing users or history.
INSERT INTO user_roles (user_id, role_id)
SELECT membership.user_id, canonical.id
  FROM user_roles AS membership
  JOIN roles AS legacy ON legacy.id = membership.role_id
  JOIN roles AS canonical
    ON canonical.tenant_id = legacy.tenant_id
   AND canonical.name = 'Administrator'
 WHERE legacy.name = 'Platform administrator'
ON CONFLICT (user_id, role_id) DO NOTHING;

DELETE FROM user_roles AS membership
 USING roles AS legacy
 WHERE membership.role_id = legacy.id
   AND legacy.name = 'Platform administrator';

DELETE FROM roles
 WHERE name = 'Platform administrator';

-- Fixed roles require MFA and use the current permission catalogue. The
-- administrator intentionally receives every catalogue permission so future
-- catalogue additions remain available to the tenant administrator.
INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
  FROM roles AS role
 CROSS JOIN permissions AS permission
 WHERE role.name = 'Administrator'
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Remove accidental widened grants from non-administrator fixed roles before
-- applying their exact least-privilege matrices. Non-fixed historical roles
-- are never changed.
DELETE FROM role_permissions AS assignment
 USING roles AS role, permissions AS permission
 WHERE assignment.role_id = role.id
   AND assignment.permission_id = permission.id
   AND role.name IN ('Operations', 'Billing', 'Support')
   AND NOT (
       (role.name = 'Operations' AND permission.name IN (
           'auth.mfa_required',
           'customer.read', 'customer.write',
           'subscription.read', 'subscription.write',
           'plan.read', 'plan.write',
           'voucher.read', 'voucher.write',
           'network.read', 'network.write',
           'session.read', 'session.write'
       ))
       OR (role.name = 'Billing' AND permission.name IN (
           'auth.mfa_required',
           'customer.read', 'customer.write',
           'subscription.read', 'subscription.write',
           'billing.read', 'billing.write',
           'voucher.read', 'voucher.write'
       ))
       OR (role.name = 'Support' AND permission.name IN (
           'auth.mfa_required',
           'customer.read', 'customer.write',
           'subscription.read', 'session.read'
       ))
   );

INSERT INTO role_permissions (role_id, permission_id)
SELECT role.id, permission.id
  FROM roles AS role
  JOIN permissions AS permission ON (
       (role.name = 'Operations' AND permission.name IN (
           'auth.mfa_required',
           'customer.read', 'customer.write',
           'subscription.read', 'subscription.write',
           'plan.read', 'plan.write',
           'voucher.read', 'voucher.write',
           'network.read', 'network.write',
           'session.read', 'session.write'
       ))
       OR (role.name = 'Billing' AND permission.name IN (
           'auth.mfa_required',
           'customer.read', 'customer.write',
           'subscription.read', 'subscription.write',
           'billing.read', 'billing.write',
           'voucher.read', 'voucher.write'
       ))
       OR (role.name = 'Support' AND permission.name IN (
           'auth.mfa_required',
           'customer.read', 'customer.write',
           'subscription.read', 'session.read'
       ))
  )
 WHERE role.name IN ('Operations', 'Billing', 'Support')
ON CONFLICT (role_id, permission_id) DO NOTHING;

CREATE TABLE staff_invitations (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email           citext      NOT NULL,
    requested_role  text        NOT NULL CHECK (requested_role IN ('Administrator', 'Operations', 'Billing', 'Support')),
    token_digest    bytea       NOT NULL CHECK (octet_length(token_digest) = 32),
    status          text        NOT NULL DEFAULT 'PENDING'
                                    CHECK (status IN ('PENDING', 'REDEEMED', 'REVOKED')),
    expires_at      timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    created_by      uuid        NOT NULL,
    sent_by         uuid,
    redeemed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    CONSTRAINT staff_invitations_creator_tenant_fkey
        FOREIGN KEY (tenant_id, created_by) REFERENCES users (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT staff_invitations_sender_tenant_fkey
        FOREIGN KEY (tenant_id, sent_by) REFERENCES users (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT staff_invitations_redemption_coherent CHECK (
        (status = 'REDEEMED' AND redeemed_at IS NOT NULL)
        OR (status IN ('PENDING', 'REVOKED') AND redeemed_at IS NULL)
    )
);

CREATE UNIQUE INDEX staff_invitations_one_pending_email_idx
    ON staff_invitations (tenant_id, email)
    WHERE status = 'PENDING';
CREATE INDEX staff_invitations_token_digest_idx
    ON staff_invitations (token_digest)
    WHERE status = 'PENDING';

CREATE TABLE staff_invitation_mfa (
    invitation_id       uuid        PRIMARY KEY,
    tenant_id           uuid        NOT NULL,
    secret_ciphertext   bytea       NOT NULL CHECK (octet_length(secret_ciphertext) > 0),
    secret_nonce        bytea       NOT NULL CHECK (octet_length(secret_nonce) = 12),
    wrapped_dek         bytea       NOT NULL CHECK (octet_length(wrapped_dek) > 0),
    kek_key_id          text        NOT NULL CHECK (length(btrim(kek_key_id)) > 0),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT staff_invitation_mfa_invitation_tenant_fkey
        FOREIGN KEY (tenant_id, invitation_id)
        REFERENCES staff_invitations (tenant_id, id) ON DELETE CASCADE
);

ALTER TABLE staff_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_invitations
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

ALTER TABLE staff_invitation_mfa ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_invitation_mfa FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_invitation_mfa
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON staff_invitations, staff_invitation_mfa TO netcore_app_rw;

ALTER TABLE user_mfa_totp
    ALTER COLUMN secret_ref DROP NOT NULL,
    ADD COLUMN secret_ciphertext bytea,
    ADD COLUMN secret_nonce bytea,
    ADD COLUMN wrapped_dek bytea,
    ADD COLUMN kek_key_id text,
    ADD CONSTRAINT user_mfa_totp_secret_representation_coherent CHECK (
        (secret_ref IS NOT NULL
            AND secret_ciphertext IS NULL
            AND secret_nonce IS NULL
            AND wrapped_dek IS NULL
            AND kek_key_id IS NULL)
        OR
        (secret_ref IS NULL
            AND octet_length(secret_ciphertext) > 0
            AND octet_length(secret_nonce) = 12
            AND octet_length(wrapped_dek) > 0
            AND length(btrim(kek_key_id)) > 0)
    );

CREATE UNIQUE INDEX customers_tenant_email_key
    ON customers (tenant_id, email)
    WHERE email IS NOT NULL;

COMMIT;
