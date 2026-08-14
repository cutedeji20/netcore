-- 0019_voucher_batch_inventory_index.up.sql
-- Supports tenant-scoped batch aggregation without touching voucher codes.

BEGIN;

CREATE INDEX vouchers_tenant_batch_idx
    ON vouchers (tenant_id, batch_id);

COMMIT;
