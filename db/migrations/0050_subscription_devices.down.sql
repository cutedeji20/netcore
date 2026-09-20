BEGIN;

DROP INDEX IF EXISTS subscriptions_device_idx;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_device_tenant_fk;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS device_id;

DROP POLICY IF EXISTS tenant_isolation ON customer_devices;
DROP TABLE IF EXISTS customer_devices;

ALTER TABLE customers DROP CONSTRAINT IF EXISTS customers_tenant_id_id_key;

COMMIT;
