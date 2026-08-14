-- 0009_subscription_list_index.up.sql
-- Supports the tenant-scoped keyset ordering used by GET /api/v1/subscriptions.

BEGIN;

CREATE INDEX subscriptions_tenant_created_id_idx
    ON subscriptions (tenant_id, created_at DESC, id DESC);

COMMIT;
