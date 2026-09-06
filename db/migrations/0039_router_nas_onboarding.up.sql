-- Router NAS onboarding keeps each RADIUS shared secret in an encrypted
-- envelope. Plaintext shared secrets never enter the database or dashboard
-- read models; the API decrypts only while rendering one-time setup material.

BEGIN;

-- Existing NAS rows predate an explicit packet-source field. Their nasname is
-- the only established RADIUS address, so retaining it as the initial source
-- preserves active routers while allowing future routers to distinguish the
-- RADIUS packet source from the NAS-IP-Address attribute.
ALTER TABLE nas
    ADD COLUMN radius_source_ip inet NOT NULL DEFAULT inet '0.0.0.0';
UPDATE nas
   SET radius_source_ip = nasname
 WHERE radius_source_ip = inet '0.0.0.0';
ALTER TABLE nas
    ALTER COLUMN radius_source_ip DROP DEFAULT;

CREATE TABLE router_radius_credentials (
    nas_id            uuid        PRIMARY KEY REFERENCES nas(id) ON DELETE CASCADE,
    tenant_id         uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    secret_ciphertext bytea       NOT NULL CHECK (octet_length(secret_ciphertext) > 0),
    secret_nonce      bytea       NOT NULL CHECK (octet_length(secret_nonce) = 12),
    wrapped_dek       bytea       NOT NULL CHECK (octet_length(wrapped_dek) > 0),
    kek_key_id        text        NOT NULL CHECK (length(btrim(kek_key_id)) > 0),
    version           bigint      NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    rotated_at        timestamptz,
    rotated_by        uuid        REFERENCES users(id) ON DELETE SET NULL,
    verified_at       timestamptz,
    verified_by       uuid        REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX router_radius_credentials_tenant_idx
    ON router_radius_credentials (tenant_id, nas_id);

ALTER TABLE router_radius_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE router_radius_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON router_radius_credentials
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- The application role is confined by the forced tenant policy. The read-only
-- reporting role intentionally receives no access to encrypted secret data.
GRANT SELECT, INSERT, UPDATE, DELETE ON router_radius_credentials TO netcore_app_rw;

COMMIT;
