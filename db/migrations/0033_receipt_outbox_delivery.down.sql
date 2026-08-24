BEGIN;

DROP FUNCTION IF EXISTS payment_receipt_reject(uuid, integer);
DROP FUNCTION IF EXISTS payment_receipt_fail(uuid, bigint, text);
DROP FUNCTION IF EXISTS payment_receipt_publish(uuid);
DROP FUNCTION IF EXISTS payment_receipt_claim(integer);

DROP INDEX IF EXISTS outbox_receipt_ready_idx;

ALTER TABLE outbox_events
    DROP COLUMN last_error,
    DROP COLUMN next_attempt_at,
    DROP COLUMN processing_started_at;

COMMIT;
