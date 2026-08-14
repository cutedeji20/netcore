-- 0020_team_permission_catalog.up.sql
-- Global capabilities assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('team.read'),
    ('team.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
