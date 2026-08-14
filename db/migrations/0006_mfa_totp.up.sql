-- 0006_mfa_totp.up.sql
-- Phase 2: tenant-isolated metadata for TOTP multi-factor authentication.
-- TOTP secret values belong in SecretStore; PostgreSQL stores only a safe
-- path-like reference that cannot contain whitespace or a copied secret.

BEGIN;

CREATE TABLE user_mfa_totp (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           uuid        NOT NULL,
    secret_ref        text        NOT NULL,
    status            text        NOT NULL DEFAULT 'PENDING'
                                      CHECK (status IN ('PENDING', 'ACTIVE', 'DISABLED')),
    last_used_counter bigint      NOT NULL DEFAULT -1
                                      CHECK (last_used_counter >= -1),
    enabled_at        timestamptz,
    disabled_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_mfa_totp_secret_ref_is_path
        CHECK (secret_ref ~ '^[a-z0-9/_.-]+$'),
    CONSTRAINT user_mfa_totp_user_tenant_fkey
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT user_mfa_totp_status_timestamps CHECK (
        (status = 'PENDING'  AND enabled_at IS NULL AND disabled_at IS NULL) OR
        (status = 'ACTIVE'   AND enabled_at IS NOT NULL AND disabled_at IS NULL) OR
        (status = 'DISABLED' AND disabled_at IS NOT NULL)
    )
);

-- Phase 2 supports one active TOTP device per user. Multiple-device and
-- recovery-code policy can be introduced later without weakening replay
-- protection for the first device.
CREATE UNIQUE INDEX user_mfa_totp_one_active_device_idx
    ON user_mfa_totp (tenant_id, user_id)
    WHERE status = 'ACTIVE';

ALTER TABLE user_mfa_totp ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_mfa_totp FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_mfa_totp
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- Operators need this table through the write application role. The reporting
-- role does not: even a secret reference is security-sensitive metadata.
GRANT SELECT, INSERT, UPDATE, DELETE ON user_mfa_totp TO netcore_app_rw;

COMMIT;
