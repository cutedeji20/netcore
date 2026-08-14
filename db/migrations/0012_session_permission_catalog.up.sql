-- 0012_session_permission_catalog.up.sql
-- Global capabilities, assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('session.read'),
    ('session.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
