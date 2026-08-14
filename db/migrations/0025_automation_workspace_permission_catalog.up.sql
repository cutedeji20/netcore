-- Global capabilities assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('automation.read'),
    ('automation.write'),
    ('workspace.read'),
    ('workspace.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
