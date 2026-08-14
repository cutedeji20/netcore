-- 0010_plan_permission_catalog.up.sql
-- Global capability names assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('plan.read'),
    ('plan.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
