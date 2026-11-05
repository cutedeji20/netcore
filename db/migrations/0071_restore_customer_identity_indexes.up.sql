-- A restored production database can retain migration history while omitting
-- non-constraint unique indexes. Customer activation uses these indexes for
-- its idempotent profile-link UPSERT, so prove the data is safe before
-- recreating them.
BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM customers
         WHERE user_id IS NOT NULL
         GROUP BY tenant_id, user_id
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION
            'cannot restore customers_tenant_user_id_key: duplicate tenant/user customer profiles exist';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM customers
         WHERE email IS NOT NULL
         GROUP BY tenant_id, email
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION
            'cannot restore customers_tenant_email_key: duplicate tenant/email customer profiles exist';
    END IF;
END;
$$;

CREATE UNIQUE INDEX IF NOT EXISTS customers_tenant_user_id_key
    ON customers (tenant_id, user_id)
    WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS customers_tenant_email_key
    ON customers (tenant_id, email)
    WHERE email IS NOT NULL;

COMMIT;
