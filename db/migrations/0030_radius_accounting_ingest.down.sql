BEGIN;

DROP FUNCTION IF EXISTS radius_accounting_ingest(
    inet, text, text, text, text, bigint, bigint, bigint, bigint, bigint,
    bigint, text, text, text
);

-- Restore the 0004 least-privilege baseline for a rollback to the prior
-- accounting implementation.
GRANT SELECT ON nas, sessions TO netcore_radius;
GRANT UPDATE (status, ended_at, close_reason, terminate_cause, last_interim_at)
    ON sessions TO netcore_radius;
GRANT INSERT ON accounting_records, nas_accounting_events TO netcore_radius;
GRANT EXECUTE ON FUNCTION quota_apply(uuid, text, timestamptz, bigint) TO netcore_radius;

COMMIT;
