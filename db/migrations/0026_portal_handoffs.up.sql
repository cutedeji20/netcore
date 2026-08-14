-- Captive-portal handoffs are short-lived, single-use bindings. The raw nonce
-- never enters PostgreSQL; only its SHA-256 digest is retained.

BEGIN;

CREATE TABLE portal_handoffs (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    subscription_id uuid        NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    nas_id          uuid        NOT NULL REFERENCES nas(id) ON DELETE RESTRICT,
    user_id         uuid        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    client_mac      text        NOT NULL CHECK (client_mac ~ '^[0-9a-f]{12}$'),
    nonce_hash      bytea       NOT NULL UNIQUE CHECK (octet_length(nonce_hash) = 32),
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT portal_handoffs_expiry_bounded CHECK (
        expires_at > created_at
        AND expires_at <= created_at + interval '120 seconds'
    )
);

ALTER TABLE portal_handoffs ENABLE ROW LEVEL SECURITY;
ALTER TABLE portal_handoffs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON portal_handoffs
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON portal_handoffs TO netcore_app_rw;

CREATE INDEX portal_handoffs_expiry_idx
    ON portal_handoffs (expires_at)
 WHERE consumed_at IS NULL;

-- FreeRADIUS needs one narrowly-scoped operation: atomically consume the
-- nonce supplied to the HotSpot while binding it to the NAS and the actual
-- client MAC in the RADIUS request. It never receives SELECT on the table.
CREATE FUNCTION radius_portal_handoff_consume(
    p_nonce      text,
    p_nas        inet,
    p_client_mac text
) RETURNS TABLE (subscription_id uuid)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_mac text;
BEGIN
    v_mac := lower(regexp_replace(p_client_mac, '[^0-9A-Fa-f]', '', 'g'));
    IF v_mac !~ '^[0-9a-f]{12}$' OR length(p_nonce) <> 43 THEN
        RETURN;
    END IF;

    RETURN QUERY
    UPDATE portal_handoffs AS handoff
       SET consumed_at = now()
      FROM nas
     WHERE handoff.nas_id = nas.id
       AND handoff.tenant_id = nas.tenant_id
       AND nas.nasname = p_nas
       AND nas.status = 'ACTIVE'
       AND handoff.nonce_hash = digest(p_nonce, 'sha256')
       AND handoff.client_mac = v_mac
       AND handoff.consumed_at IS NULL
       AND handoff.expires_at > now()
 RETURNING handoff.subscription_id;
END;
$$;

REVOKE ALL ON FUNCTION radius_portal_handoff_consume(text, inet, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_portal_handoff_consume(text, inet, text) TO netcore_radius;

COMMIT;
