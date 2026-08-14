BEGIN;

DROP FUNCTION IF EXISTS payment_webhook_audit(uuid, text, jsonb);
DROP FUNCTION IF EXISTS payment_webhook_owner(text, text);
DROP INDEX IF EXISTS webhook_events_ready_idx;

-- A rollback must not leave a claim permanently stranded.
UPDATE webhook_events SET status = 'FAILED' WHERE status = 'PROCESSING';

ALTER TABLE webhook_events
    DROP CONSTRAINT IF EXISTS webhook_events_reference_len,
    DROP CONSTRAINT IF EXISTS webhook_events_payload_hash_len,
    DROP CONSTRAINT IF EXISTS webhook_events_status_check,
    ADD CONSTRAINT webhook_events_status_check
        CHECK (status IN ('RECEIVED','PROCESSED','FAILED','IGNORED')),
    DROP COLUMN next_attempt_at,
    DROP COLUMN processing_started_at,
    DROP COLUMN provider_reference;

COMMIT;
