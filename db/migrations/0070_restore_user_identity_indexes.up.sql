-- A restored production database can have the foundational migration recorded
-- while its non-constraint unique indexes are absent. Registration relies on
-- these indexes for its tenant-scoped UPSERT, so restore them idempotently.
BEGIN;

CREATE UNIQUE INDEX IF NOT EXISTS users_tenant_email_key
    ON users (tenant_id, email)
    WHERE email IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS users_tenant_phone_key
    ON users (tenant_id, phone)
    WHERE phone IS NOT NULL;

COMMIT;
