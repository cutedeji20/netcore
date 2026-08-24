BEGIN;

ALTER TABLE outbox_events
    ADD COLUMN processing_started_at timestamptz,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN last_error text;

CREATE INDEX outbox_receipt_ready_idx
    ON outbox_events (next_attempt_at, created_at)
    WHERE event_type = 'payment.receipt.requested' AND published_at IS NULL;

-- Receipt events have a tenant key, but the worker intentionally has no
-- browser/request tenant context. Forced RLS would therefore make a direct
-- worker scan return no rows. Keep the one permitted global queue operation
-- behind narrow SECURITY DEFINER procedures rather than letting the worker
-- bypass tenant isolation generally.
CREATE FUNCTION payment_receipt_claim(p_max_attempts integer)
RETURNS TABLE (
    event_id uuid,
    attempts integer,
    payment_id uuid,
    customer_id uuid,
    recipient_email text,
    plan_name text,
    reference text,
    amount_minor bigint,
    currency text,
    starts_at timestamptz,
    expires_at timestamptz,
    payload jsonb
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
BEGIN
    IF p_max_attempts < 1 THEN
        RAISE EXCEPTION 'receipt maximum attempts must be positive'
            USING ERRCODE = '22023';
    END IF;

    UPDATE outbox_events
       SET processing_started_at = NULL,
           next_attempt_at = now(),
           last_error = 'worker claim lease expired'
     WHERE event_type = 'payment.receipt.requested'
       AND published_at IS NULL
       AND processing_started_at < now() - interval '5 minutes';

    RETURN QUERY
    WITH candidate AS (
        SELECT e.event_id,
               p.id AS payment_id,
               c.id AS customer_id,
               COALESCE(NULLIF(c.email::text, ''), u.email::text, '') AS recipient_email,
               plan.name AS plan_name,
               p.provider_reference AS reference,
               p.amount_minor,
               p.currency::text AS currency,
               s.starts_at,
               s.expires_at,
               e.payload
          FROM outbox_events AS e
          JOIN payments AS p
            ON p.id = e.aggregate_id
           AND p.tenant_id = e.tenant_id
           AND p.status = 'SUCCESS'
          JOIN subscriptions AS s
            ON s.id = p.subscription_id
           AND s.tenant_id = p.tenant_id
           AND s.status = 'ACTIVE'
          JOIN customers AS c
            ON c.id = p.customer_id
           AND c.tenant_id = p.tenant_id
          JOIN users AS u
            ON u.id = c.user_id
           AND u.tenant_id = c.tenant_id
          JOIN plans AS plan
            ON plan.id = s.plan_id
           AND plan.tenant_id = s.tenant_id
         WHERE e.event_type = 'payment.receipt.requested'
           AND e.published_at IS NULL
           AND e.next_attempt_at <= now()
           AND e.attempts < p_max_attempts
           AND (e.processing_started_at IS NULL OR e.processing_started_at < now() - interval '5 minutes')
         ORDER BY e.next_attempt_at, e.created_at, e.id
         FOR UPDATE OF e SKIP LOCKED
         LIMIT 1
    ), claimed AS (
        UPDATE outbox_events AS e
           SET attempts = e.attempts + 1,
               processing_started_at = now(),
               last_error = NULL
          FROM candidate AS c
         WHERE e.event_id = c.event_id
           AND e.event_type = 'payment.receipt.requested'
           AND e.published_at IS NULL
        RETURNING e.event_id, e.attempts
    )
    SELECT c.event_id,
           claimed.attempts,
           c.payment_id,
           c.customer_id,
           c.recipient_email,
           c.plan_name,
           c.reference,
           c.amount_minor,
           c.currency,
           c.starts_at,
           c.expires_at,
           c.payload
      FROM candidate AS c
      JOIN claimed ON claimed.event_id = c.event_id;
END;
$$;

CREATE FUNCTION payment_receipt_publish(p_event_id uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE changed integer;
BEGIN
    UPDATE outbox_events
       SET published_at = now(),
           processing_started_at = NULL,
           last_error = NULL
     WHERE event_id = p_event_id
       AND event_type = 'payment.receipt.requested'
       AND published_at IS NULL
       AND processing_started_at IS NOT NULL;
    GET DIAGNOSTICS changed = ROW_COUNT;
    RETURN changed = 1;
END;
$$;

CREATE FUNCTION payment_receipt_fail(
    p_event_id uuid,
    p_delay_seconds bigint,
    p_last_error text
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE changed integer;
BEGIN
    IF p_delay_seconds < 0 OR p_delay_seconds > 300 THEN
        RAISE EXCEPTION 'receipt retry delay is outside the allowed bound'
            USING ERRCODE = '22023';
    END IF;
    UPDATE outbox_events
       SET processing_started_at = NULL,
           next_attempt_at = now() + p_delay_seconds * interval '1 second',
           last_error = left(replace(replace(COALESCE(p_last_error, 'receipt processing failed'), E'\n', ' '), E'\r', ' '), 240)
     WHERE event_id = p_event_id
       AND event_type = 'payment.receipt.requested'
       AND published_at IS NULL
       AND processing_started_at IS NOT NULL;
    GET DIAGNOSTICS changed = ROW_COUNT;
    RETURN changed = 1;
END;
$$;

CREATE FUNCTION payment_receipt_reject(p_event_id uuid, p_max_attempts integer)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE changed integer;
BEGIN
    IF p_max_attempts < 1 THEN
        RAISE EXCEPTION 'receipt maximum attempts must be positive'
            USING ERRCODE = '22023';
    END IF;
    UPDATE outbox_events
       SET attempts = GREATEST(attempts, p_max_attempts),
           processing_started_at = NULL,
           last_error = 'receipt payload did not match verified payment'
     WHERE event_id = p_event_id
       AND event_type = 'payment.receipt.requested'
       AND published_at IS NULL;
    GET DIAGNOSTICS changed = ROW_COUNT;
    RETURN changed = 1;
END;
$$;

REVOKE ALL ON FUNCTION payment_receipt_claim(integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION payment_receipt_publish(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION payment_receipt_fail(uuid, bigint, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION payment_receipt_reject(uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION payment_receipt_claim(integer) TO netcore_app_rw;
GRANT EXECUTE ON FUNCTION payment_receipt_publish(uuid) TO netcore_app_rw;
GRANT EXECUTE ON FUNCTION payment_receipt_fail(uuid, bigint, text) TO netcore_app_rw;
GRANT EXECUTE ON FUNCTION payment_receipt_reject(uuid, integer) TO netcore_app_rw;

COMMIT;
