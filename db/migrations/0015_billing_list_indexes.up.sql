-- 0015_billing_list_indexes.up.sql
-- Supports the recorded-at keyset order of the unified billing list.

BEGIN;

CREATE INDEX payments_tenant_recorded_id_idx
    ON payments (tenant_id, (COALESCE(verified_at, created_at)) DESC, id DESC);

CREATE INDEX invoices_tenant_recorded_id_idx
    ON invoices (tenant_id, (COALESCE(paid_at, issued_at, created_at)) DESC, id DESC);

COMMIT;
