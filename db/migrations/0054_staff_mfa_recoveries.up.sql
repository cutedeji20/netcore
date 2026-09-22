-- A password reset can revoke a staff member's previous authenticator without
-- losing the account.  The raw recovery token is never persisted.
BEGIN;

CREATE TABLE staff_mfa_recoveries (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL,
    email             citext NOT NULL,
    token_digest      bytea NOT NULL CHECK (octet_length(token_digest)=32),
    status            text NOT NULL DEFAULT 'DELIVERY_PENDING' CHECK (status IN ('DELIVERY_PENDING','PENDING','REDEEMED','REVOKED')),
    expires_at        timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    created_by        uuid NOT NULL,
    redeemed_at       timestamptz,
    secret_ciphertext bytea,
    secret_nonce      bytea,
    wrapped_dek       bytea,
    kek_key_id        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id,id),
    CONSTRAINT staff_mfa_recoveries_user_tenant_fkey FOREIGN KEY (tenant_id,user_id) REFERENCES users(tenant_id,id) ON DELETE CASCADE,
    CONSTRAINT staff_mfa_recoveries_creator_tenant_fkey FOREIGN KEY (tenant_id,created_by) REFERENCES users(tenant_id,id) ON DELETE RESTRICT,
    CONSTRAINT staff_mfa_recoveries_redemption_coherent CHECK ((status='REDEEMED' AND redeemed_at IS NOT NULL) OR (status IN ('DELIVERY_PENDING','PENDING','REVOKED') AND redeemed_at IS NULL)),
    CONSTRAINT staff_mfa_recoveries_secret_coherent CHECK ((secret_ciphertext IS NULL AND secret_nonce IS NULL AND wrapped_dek IS NULL AND kek_key_id IS NULL) OR (octet_length(secret_ciphertext)>0 AND octet_length(secret_nonce)=12 AND octet_length(wrapped_dek)>0 AND length(btrim(kek_key_id))>0))
);
CREATE UNIQUE INDEX staff_mfa_recoveries_one_pending_user_idx ON staff_mfa_recoveries(tenant_id,user_id) WHERE status='PENDING';
CREATE INDEX staff_mfa_recoveries_digest_idx ON staff_mfa_recoveries(token_digest) WHERE status='PENDING';

ALTER TABLE staff_mfa_recoveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_mfa_recoveries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_mfa_recoveries USING (tenant_id=current_tenant_id()) WITH CHECK (tenant_id=current_tenant_id());
GRANT SELECT, INSERT, UPDATE, DELETE ON staff_mfa_recoveries TO netcore_app_rw;

CREATE FUNCTION staff_mfa_recovery_tenant_for_digest(p_digest bytea)
RETURNS uuid LANGUAGE sql SECURITY DEFINER SET search_path=public,pg_temp SET row_security=off AS $$
  SELECT tenant_id FROM staff_mfa_recoveries WHERE token_digest=p_digest AND status='PENDING' AND expires_at>now() LIMIT 1
$$;
REVOKE ALL ON FUNCTION staff_mfa_recovery_tenant_for_digest(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION staff_mfa_recovery_tenant_for_digest(bytea) TO netcore_app_rw;
COMMIT;
