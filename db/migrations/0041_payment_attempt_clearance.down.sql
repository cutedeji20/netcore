BEGIN;

DROP INDEX IF EXISTS payments_uncleared_billing_idx;
ALTER TABLE payments
    DROP COLUMN IF EXISTS cleared_by,
    DROP COLUMN IF EXISTS cleared_at;

COMMIT;
