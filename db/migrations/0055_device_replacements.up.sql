-- One verified replacement may wait for a real RADIUS observation. Browser
-- context alone never changes subscriptions.device_id.
BEGIN;

CREATE TABLE device_replacements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    subscription_id uuid NOT NULL REFERENCES subscriptions(id) ON DELETE RESTRICT,
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    old_device_id uuid NOT NULL REFERENCES devices(id) ON DELETE RESTRICT,
    target_device_id uuid REFERENCES devices(id) ON DELETE RESTRICT,
    nas_id uuid NOT NULL REFERENCES nas(id) ON DELETE RESTRICT,
    target_mac text NOT NULL CHECK (target_mac ~ '^[0-9a-f]{12}$'),
    challenge_id text NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('CHALLENGE','PENDING','CONSUMED','CANCELLED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    verified_at timestamptz,
    consumed_at timestamptz,
    CONSTRAINT device_replacement_expiry CHECK (expires_at>created_at AND expires_at<=created_at+interval '10 minutes'),
    CONSTRAINT device_replacement_state CHECK ((status='CHALLENGE' AND target_device_id IS NULL AND verified_at IS NULL AND consumed_at IS NULL) OR (status='PENDING' AND target_device_id IS NOT NULL AND verified_at IS NOT NULL AND consumed_at IS NULL) OR (status='CONSUMED' AND target_device_id IS NOT NULL AND verified_at IS NOT NULL AND consumed_at IS NOT NULL) OR status='CANCELLED')
);
CREATE UNIQUE INDEX device_replacements_one_open ON device_replacements(tenant_id,subscription_id) WHERE status IN ('CHALLENGE','PENDING');
CREATE INDEX device_replacements_expiry ON device_replacements(expires_at) WHERE status IN ('CHALLENGE','PENDING');
ALTER TABLE device_replacements ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_replacements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON device_replacements USING (tenant_id=current_tenant_id()) WITH CHECK (tenant_id=current_tenant_id());
GRANT SELECT,INSERT,UPDATE ON device_replacements TO netcore_app_rw;

-- Application code needs only the answer to whether a live reservation exists,
-- not direct access to RADIUS accounting/reservation rows.
CREATE FUNCTION subscription_has_radius_reservation(p_tenant uuid,p_subscription uuid)
RETURNS boolean LANGUAGE sql SECURITY DEFINER STABLE
SET search_path=public,pg_temp AS $$
 SELECT p_tenant=current_tenant_id()
    AND EXISTS(SELECT 1 FROM subscriptions s WHERE s.tenant_id=p_tenant AND s.id=p_subscription)
    AND EXISTS(SELECT 1 FROM radius_access_reservations r
                WHERE r.tenant_id=p_tenant AND r.subscription_id=p_subscription AND r.expires_at>now());
$$;
REVOKE ALL ON FUNCTION subscription_has_radius_reservation(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION subscription_has_radius_reservation(uuid,uuid) TO netcore_app_rw;

COMMIT;
