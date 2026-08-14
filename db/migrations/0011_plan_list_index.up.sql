-- 0011_plan_list_index.up.sql
-- Supports the plan catalogue list and its active-subscription aggregate.

BEGIN;

CREATE INDEX plans_tenant_created_id_idx
    ON plans (tenant_id, created_at DESC, id DESC);

CREATE INDEX subscriptions_tenant_status_plan_idx
    ON subscriptions (tenant_id, status, plan_id);

COMMIT;
