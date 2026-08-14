-- 0016_network_permission_catalog.up.sql
-- Global capabilities assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('network.read'),
    ('network.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
