BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM role_permissions AS rp
          JOIN permissions AS p ON p.id = rp.permission_id
         WHERE p.name IN ('session.read', 'session.write')
    ) THEN
        RAISE EXCEPTION 'cannot remove session permissions while roles still use them';
    END IF;
END;
$$;

DELETE FROM permissions
 WHERE name IN ('session.read', 'session.write');

COMMIT;
