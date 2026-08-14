BEGIN;

DROP INDEX IF EXISTS subscriptions_tenant_status_plan_idx;
DROP INDEX IF EXISTS plans_tenant_created_id_idx;

COMMIT;
