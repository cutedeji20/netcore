-- 0029_payment_webhook_queue.up.sql
-- Signed webhook ingress stores only the provider event identity, reference,
-- and raw-body digest. A worker later verifies the transaction independently;
-- webhook JSON is never a source of payment facts or stored as customer data.

BEGIN;

ALTER TABLE webhook_events
    ADD COLUMN provider_reference text,
    ADD COLUMN processing_started_at timestamptz,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE webhook_events
    DROP CONSTRAINT webhook_events_status_check,
    ADD CONSTRAINT webhook_events_status_check
        CHECK (status IN ('RECEIVED','PROCESSING','PROCESSED','FAILED','IGNORED')),
    ADD CONSTRAINT webhook_events_payload_hash_len
        CHECK (octet_length(payload_hash) = 32),
    ADD CONSTRAINT webhook_events_reference_len
        CHECK (provider_reference IS NULL OR char_length(provider_reference) BETWEEN 1 AND 200);

CREATE INDEX webhook_events_ready_idx
    ON webhook_events (provider, next_attempt_at, received_at)
    WHERE status IN ('RECEIVED','FAILED');

-- The public webhook has no tenant identity. Resolve ownership only from the
-- gateway/reference pair that was frozen in an existing payment record; this
-- function is the sole RLS crossing for the worker before it opens a tenant
-- transaction and calls the normal server-to-server verification service.
CREATE FUNCTION payment_webhook_owner(
    p_gateway   text,
    p_reference text
) RETURNS TABLE (
    tenant_id uuid,
    user_id   uuid
)
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public, pg_temp
AS $$
    SELECT p.tenant_id, c.user_id
      FROM payments AS p
      JOIN customers AS c
        ON c.id = p.customer_id
       AND c.tenant_id = p.tenant_id
     WHERE p.gateway = p_gateway
       AND p.provider_reference = p_reference;
$$;

REVOKE ALL ON FUNCTION payment_webhook_owner(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION payment_webhook_owner(text, text) TO netcore_app_rw;

COMMENT ON FUNCTION payment_webhook_owner IS
'Maps a signed provider reference to the frozen payment owner without exposing '
'payment data to the unscoped webhook queue. Caller must still use tenant RLS.';

-- The queue has no tenant at ingress, while audit_logs has forced RLS. Keep
-- the two permitted unscoped audit facts behind a fixed SECURITY DEFINER
-- writer instead of teaching the worker to bypass the audit RLS policy.
CREATE FUNCTION payment_webhook_audit(
    p_event_id uuid,
    p_action   text,
    p_metadata jsonb DEFAULT '{}'::jsonb
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
BEGIN
    IF p_action NOT IN ('WEBHOOK_REPLAY_DETECTED', 'PAYMENT_WEBHOOK_IGNORED') THEN
        RAISE EXCEPTION 'unsupported payment webhook audit action'
            USING ERRCODE = '22023';
    END IF;
    INSERT INTO audit_logs (actor_type, action, resource_type, resource_id, metadata)
    VALUES ('GATEWAY', p_action, 'webhook_event', p_event_id, p_metadata);
END;
$$;

REVOKE ALL ON FUNCTION payment_webhook_audit(uuid, text, jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION payment_webhook_audit(uuid, text, jsonb) TO netcore_app_rw;

COMMIT;
