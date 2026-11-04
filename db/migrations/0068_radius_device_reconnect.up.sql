-- Reauthorize an enrolled device after a coverage break without counting its
-- still-closing prior session as another device. RouterOS asks RADIUS again on
-- every reconnect; this does not create router-local indefinite access.
BEGIN;

CREATE OR REPLACE FUNCTION radius_device_mac_authorize(
    p_nas inet, p_client_mac text, p_at timestamptz DEFAULT now()
) RETURNS TABLE (
    subscription_id uuid, rate_limit text, session_timeout_seconds integer,
    idle_timeout_seconds integer, interim_interval integer, total_limit bigint,
    total_limit_gigawords bigint, filter_id text
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_subscription_id uuid;
    v_device_id uuid;
    v_mac text;
    v_active_sessions integer;
    v_reserved_sessions integer;
    v_max_sessions integer;
    v_policy record;
BEGIN
    v_mac := lower(regexp_replace(COALESCE(p_client_mac, ''), '[^0-9A-Fa-f]', '', 'g'));
    IF p_nas IS NULL OR v_mac !~ '^[0-9a-f]{12}$' THEN RETURN; END IF;

    SELECT subscription.id, device_row.id, plan.max_concurrent_sessions
      INTO v_subscription_id, v_device_id, v_max_sessions
      FROM nas
      JOIN devices AS device_row
        ON device_row.tenant_id = nas.tenant_id
       AND device_row.normalized_mac = v_mac
       AND device_row.status = 'ACTIVE'
      JOIN customers AS customer
        ON customer.id = device_row.customer_id
       AND customer.tenant_id = device_row.tenant_id
       AND customer.status = 'ACTIVE'
      JOIN subscriptions AS subscription
        ON subscription.tenant_id = device_row.tenant_id
       AND subscription.customer_id = device_row.customer_id
       AND subscription.device_id = device_row.id
       AND subscription.status = 'ACTIVE'
       AND subscription.starts_at <= p_at
       AND subscription.expires_at > p_at
      JOIN plans AS plan ON plan.id = subscription.plan_id AND plan.tenant_id = subscription.tenant_id
      JOIN LATERAL radius_portal_access_policy(subscription.id, p_at) AS eligible_policy ON true
     WHERE nas.nasname = p_nas AND nas.status = 'ACTIVE'
     ORDER BY subscription.expires_at DESC
     LIMIT 1
     FOR UPDATE OF device_row, subscription;
    IF NOT FOUND THEN RETURN; END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(v_subscription_id::text, 0));
    PERFORM 1 FROM usage_counters AS quota
     WHERE quota.subscription_id = v_subscription_id
       AND quota.period_start <= p_at AND quota.period_end > p_at
     FOR UPDATE;
    SELECT * INTO v_policy FROM radius_portal_access_policy(v_subscription_id, p_at);
    IF NOT FOUND THEN RETURN; END IF;

    DELETE FROM radius_access_reservations WHERE expires_at <= p_at;
    SELECT count(*) INTO v_active_sessions
      FROM sessions AS active_session
     WHERE active_session.subscription_id = v_subscription_id
       AND active_session.status <> 'CLOSED'
       AND active_session.device_id IS DISTINCT FROM v_device_id;
    SELECT count(*) INTO v_reserved_sessions
      FROM radius_access_reservations AS reservation
     WHERE reservation.subscription_id = v_subscription_id
       AND reservation.expires_at > p_at
       AND reservation.normalized_mac <> v_mac;
    IF v_active_sessions + v_reserved_sessions >= v_max_sessions THEN RETURN; END IF;

    INSERT INTO radius_access_reservations (tenant_id, subscription_id, normalized_mac, expires_at)
    SELECT tenant_id, id, v_mac, p_at + interval '120 seconds'
      FROM subscriptions WHERE id = v_subscription_id
    -- Preserve an existing same-MAC reservation for its original bounded TTL.
    -- A retry must not become a unique-key error or extend that safety bound.
    ON CONFLICT (subscription_id, normalized_mac) DO NOTHING;

    UPDATE devices
       SET last_seen_at = GREATEST(COALESCE(last_seen_at, p_at), p_at), updated_at = now()
     WHERE id = v_device_id AND normalized_mac = v_mac;

    RETURN QUERY SELECT v_subscription_id, v_policy.rate_limit, v_policy.session_timeout_seconds,
      v_policy.idle_timeout_seconds, v_policy.interim_interval, v_policy.total_limit,
      v_policy.total_limit_gigawords, v_policy.filter_id;
END;
$$;

REVOKE ALL ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) TO netcore_radius;

COMMENT ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) IS
'Authorizes automatic reconnect only for the exact active enrolled device and live entitlement. Existing sessions or reservations for that same device do not consume another concurrent-device slot.';

COMMIT;
