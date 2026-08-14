-- 0007_customer_list_index.up.sql
-- Supports the tenant-scoped keyset ordering used by GET /api/v1/customers.
-- The existing unique customer-number index serves exact account lookups;
-- this index keeps the staff list predictable as a tenant grows.

BEGIN;

CREATE INDEX customers_tenant_created_id_idx
    ON customers (tenant_id, created_at DESC, id DESC);

COMMIT;
