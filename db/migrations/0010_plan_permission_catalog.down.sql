BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM role_permissions AS rp
          JOIN permissions AS p ON p.id = rp.permission_id
         WHERE p.name IN ('plan.read', 'plan.write')
    ) THEN
        RAISE EXCEPTION 'cannot remove plan permissions while roles still use them';
    END IF;
END;
$$;

DELETE FROM permissions
 WHERE name IN ('plan.read', 'plan.write');

COMMIT;
