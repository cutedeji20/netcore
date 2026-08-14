-- 0018_voucher_permission_catalog.up.sql
-- Global capabilities assigned through tenant-scoped roles.

BEGIN;

INSERT INTO permissions (name)
VALUES
    ('voucher.read'),
    ('voucher.write')
ON CONFLICT (name) DO NOTHING;

COMMIT;
