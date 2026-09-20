BEGIN;

DROP INDEX IF EXISTS subscriptions_device_idx;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_device_tenant_fk;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS device_id;

ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_tenant_id_id_key;

COMMIT;
