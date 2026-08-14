-- 0014_billing_permission_catalog.up.sql
-- Global capabilities assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('billing.read'),
    ('billing.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
