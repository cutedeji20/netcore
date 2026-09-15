BEGIN;

ALTER TABLE tenants
    ADD COLUMN require_email_verification boolean NOT NULL DEFAULT false,
    ADD COLUMN require_phone_verification boolean NOT NULL DEFAULT false;

COMMIT;
