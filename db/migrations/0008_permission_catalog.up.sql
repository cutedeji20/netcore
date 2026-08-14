-- 0008_permission_catalog.up.sql
-- Permissions are global capability names; assignments remain tenant-scoped
-- through roles and role_permissions.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('customer.read'),
    ('customer.write'),
    ('subscription.read'),
    ('subscription.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
