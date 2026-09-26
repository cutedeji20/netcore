BEGIN;
DROP FUNCTION IF EXISTS subscription_has_radius_reservation(uuid,uuid);
DROP POLICY IF EXISTS tenant_isolation ON device_replacements;
DROP TABLE IF EXISTS device_replacements;
COMMIT;
