-- Dashboard-managed Resend and Paystack credentials are tenant-scoped
-- encrypted envelopes. This table intentionally has no plaintext credential
-- column: a database export alone cannot call either provider.

BEGIN;

CREATE TABLE integration_providers (
    id                    uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    provider              text        NOT NULL CHECK (provider IN ('resend', 'paystack')),
    status                text        NOT NULL DEFAULT 'DISCONNECTED'
                                      CHECK (status IN ('DISCONNECTED', 'DISABLED', 'ACTIVE')),
    credential_ciphertext bytea,
    credential_nonce      bytea,
    wrapped_dek           bytea,
    kek_key_id            text,
    sender_email          citext,
    paystack_mode         text        CHECK (paystack_mode IN ('TEST', 'LIVE')),
    last_tested_at        timestamptz,
    last_test_succeeded   boolean,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    activated_at          timestamptz,
    disabled_at           timestamptz,
    updated_by            uuid        REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE (tenant_id, provider),

    -- An active or disabled record has a complete encryption envelope. A
    -- disconnected record retains no credential material after deletion.
    CONSTRAINT integration_providers_envelope_coherent CHECK (
        (status = 'DISCONNECTED'
            AND credential_ciphertext IS NULL
            AND credential_nonce IS NULL
            AND wrapped_dek IS NULL
            AND kek_key_id IS NULL)
        OR
        (status IN ('ACTIVE', 'DISABLED')
            AND octet_length(credential_ciphertext) > 0
            AND octet_length(credential_nonce) = 12
            AND octet_length(wrapped_dek) > 0
            AND length(btrim(kek_key_id)) > 0)
    ),

    -- Provider-specific safe metadata is kept separate from the ciphertext.
    -- The e-mail sender never belongs to Paystack; mode never belongs to
    -- Resend, preventing an ambiguous record from reaching runtime code.
    CONSTRAINT integration_providers_provider_metadata_coherent CHECK (
        (provider = 'resend' AND sender_email IS NOT NULL AND paystack_mode IS NULL)
        OR
        (provider = 'paystack' AND sender_email IS NULL AND paystack_mode IS NOT NULL)
    ),

    CONSTRAINT integration_providers_status_timestamps_coherent CHECK (
        (status = 'ACTIVE' AND activated_at IS NOT NULL AND disabled_at IS NULL)
        OR
        (status = 'DISABLED' AND disabled_at IS NOT NULL)
        OR
        (status = 'DISCONNECTED' AND activated_at IS NULL AND disabled_at IS NULL)
    ),

    CONSTRAINT integration_providers_test_result_coherent CHECK (
        (last_tested_at IS NULL AND last_test_succeeded IS NULL)
        OR
        (last_tested_at IS NOT NULL AND last_test_succeeded IS NOT NULL)
    )
);

CREATE INDEX integration_providers_active_tenant_idx
    ON integration_providers (tenant_id, provider)
    WHERE status = 'ACTIVE';

ALTER TABLE integration_providers ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_providers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON integration_providers
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- 0004's schema-wide grant predates this table. Grant the same constrained
-- application DML capability explicitly; RLS still confines every statement
-- to the request tenant.
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_providers TO netcore_app_rw;

INSERT INTO permissions (name)
VALUES
    ('integration.read'),
    ('integration.write')
ON CONFLICT (name) DO NOTHING;

-- Existing staff roles already able to alter workspace settings receive both
-- integration permissions. Customer roles do not inherit either permission.
INSERT INTO role_permissions (role_id, permission_id)
SELECT DISTINCT existing.role_id, integration_permission.id
  FROM role_permissions AS existing
  JOIN permissions AS workspace_permission
    ON workspace_permission.id = existing.permission_id
   AND workspace_permission.name = 'workspace.write'
 CROSS JOIN (
    SELECT id
      FROM permissions
     WHERE name IN ('integration.read', 'integration.write')
 ) AS integration_permission
ON CONFLICT (role_id, permission_id) DO NOTHING;

COMMIT;
