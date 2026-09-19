BEGIN;

CREATE TABLE tenant_hotspot_tethering_policies (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    enabled boolean NOT NULL DEFAULT false,
    expected_client_ttl integer NOT NULL DEFAULT 64 CHECK (expected_client_ttl BETWEEN 64 AND 255),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE RESTRICT
);

INSERT INTO tenant_hotspot_tethering_policies (tenant_id)
SELECT id FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;

CREATE FUNCTION create_tenant_hotspot_tethering_policy() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
BEGIN
    INSERT INTO tenant_hotspot_tethering_policies (tenant_id) VALUES (NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_create_hotspot_tethering_policy
    AFTER INSERT ON tenants
    FOR EACH ROW EXECUTE FUNCTION create_tenant_hotspot_tethering_policy();

ALTER TABLE tenant_hotspot_tethering_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_hotspot_tethering_policies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_hotspot_tethering_policies
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_hotspot_tethering_policies TO netcore_app_rw;

COMMIT;
