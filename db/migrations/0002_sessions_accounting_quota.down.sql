BEGIN;
DROP FUNCTION IF EXISTS quota_reset(uuid, timestamptz, timestamptz, bigint);
DROP FUNCTION IF EXISTS quota_apply(uuid, text, timestamptz, bigint);
DROP TABLE IF EXISTS usage_counters, nas_accounting_events, accounting_records, sessions CASCADE;
COMMIT;
