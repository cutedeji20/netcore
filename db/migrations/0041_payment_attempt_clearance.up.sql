BEGIN;

-- Cleared attempts remain financial/audit facts. They are never hard-deleted,
-- and cannot later be activated by a delayed gateway callback.
ALTER TABLE payments
    ADD COLUMN cleared_at timestamptz,
    ADD COLUMN cleared_by uuid REFERENCES users(id) ON DELETE RESTRICT;

CREATE INDEX payments_uncleared_billing_idx
    ON payments (tenant_id, created_at DESC)
    WHERE cleared_at IS NULL;

COMMIT;
