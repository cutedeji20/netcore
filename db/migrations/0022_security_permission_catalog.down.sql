BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM role_permissions AS rp
          JOIN permissions AS p ON p.id = rp.permission_id
         WHERE p.name = 'security.read'
    ) THEN
        RAISE EXCEPTION 'cannot remove security.read while roles still use it';
    END IF;
END;
$$;

DELETE FROM permissions WHERE name = 'security.read';

COMMIT;
