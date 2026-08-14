-- 0013_session_list_index.up.sql
-- Supports tenant-scoped session list ordering and current-usage reads.

BEGIN;

CREATE INDEX sessions_tenant_started_id_idx
    ON sessions (tenant_id, started_at DESC, id DESC);

CREATE INDEX usage_counters_tenant_subscription_period_idx
    ON usage_counters (tenant_id, subscription_id, period_start DESC, period_end);

COMMIT;
