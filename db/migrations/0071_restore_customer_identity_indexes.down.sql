BEGIN;

DROP INDEX IF EXISTS customers_tenant_email_key;
DROP INDEX IF EXISTS customers_tenant_user_id_key;

COMMIT;
