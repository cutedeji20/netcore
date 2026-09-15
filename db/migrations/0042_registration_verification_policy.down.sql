BEGIN;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS require_phone_verification,
    DROP COLUMN IF EXISTS require_email_verification;

COMMIT;
