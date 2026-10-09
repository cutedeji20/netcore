BEGIN;

CREATE TABLE reseller_designations (
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    PRIMARY KEY (tenant_id, user_id)
);
ALTER TABLE reseller_designations ENABLE ROW LEVEL SECURITY;
ALTER TABLE reseller_designations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON reseller_designations
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());
GRANT SELECT, INSERT, DELETE ON reseller_designations TO netcore_app_rw;

COMMIT;
