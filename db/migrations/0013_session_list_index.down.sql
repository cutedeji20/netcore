BEGIN;

DROP INDEX IF EXISTS usage_counters_tenant_subscription_period_idx;
DROP INDEX IF EXISTS sessions_tenant_started_id_idx;

COMMIT;
