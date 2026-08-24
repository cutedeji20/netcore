BEGIN;

DROP POLICY IF EXISTS tenant_isolation ON integration_providers;
ALTER TABLE IF EXISTS integration_providers DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS integration_providers;

DELETE FROM role_permissions
 WHERE permission_id IN (
    SELECT id FROM permissions WHERE name IN ('integration.read', 'integration.write')
 );
DELETE FROM permissions WHERE name IN ('integration.read', 'integration.write');

COMMIT;
