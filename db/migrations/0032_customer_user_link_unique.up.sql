-- A customer login can own exactly one customer profile in a tenant. This
-- turns verification retries into an idempotent operation rather than a way
-- to manufacture duplicate customer records.

BEGIN;

CREATE UNIQUE INDEX customers_tenant_user_id_key
    ON customers (tenant_id, user_id)
    WHERE user_id IS NOT NULL;

COMMIT;
