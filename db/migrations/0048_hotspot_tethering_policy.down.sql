BEGIN;
DROP TRIGGER tenants_create_hotspot_tethering_policy ON tenants;
DROP FUNCTION create_tenant_hotspot_tethering_policy();
DROP TABLE tenant_hotspot_tethering_policies;
COMMIT;
