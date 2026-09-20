-- Browserless HotSpot/POS access. RouterOS MAC authentication is safe only
-- when RADIUS accepts an already-owned, active device-bound subscription.
BEGIN;

CREATE FUNCTION radius_device_mac_authorize(
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
    v_mac text;
    v_active_sessions integer;
    v_reserved_sessions integer;
    v_max_sessions integer;
BEGIN
    v_mac := lower(regexp_replace(COALESCE(p_client_mac, ''), '[^0-9A-Fa-f]', '', 'g'));
    IF p_nas IS NULL OR v_mac !~ '^[0-9a-f]{12}$' THEN RETURN; END IF;

    -- A MAC is never sufficient by itself: it must be owned by an active
    -- device, linked to an active entitlement, and arrive through its active NAS.
    SELECT subscription.id, plan.max_concurrent_sessions
      INTO v_subscription_id, v_max_sessions
      FROM nas
      JOIN devices AS device_row
        ON device_row.tenant_id = nas.tenant_id
       AND device_row.normalized_mac = v_mac
       AND device_row.status = 'ACTIVE'
      JOIN subscriptions AS subscription
        ON subscription.tenant_id = device_row.tenant_id
       AND subscription.customer_id = device_row.customer_id
       AND subscription.device_id = device_row.id
       AND subscription.status = 'ACTIVE'
       AND subscription.starts_at <= p_at
       AND subscription.expires_at > p_at
      JOIN plans AS plan ON plan.id = subscription.plan_id AND plan.tenant_id = subscription.tenant_id
     WHERE nas.nasname = p_nas AND nas.status = 'ACTIVE'
     ORDER BY subscription.expires_at DESC
     LIMIT 1
     FOR UPDATE OF device_row;
    IF NOT FOUND THEN RETURN; END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(v_subscription_id::text, 0));
    DELETE FROM radius_access_reservations WHERE expires_at <= p_at;
    SELECT count(*) INTO v_active_sessions FROM sessions
     WHERE subscription_id = v_subscription_id AND status <> 'CLOSED';
    SELECT count(*) INTO v_reserved_sessions FROM radius_access_reservations
     WHERE subscription_id = v_subscription_id AND expires_at > p_at;
    IF v_active_sessions + v_reserved_sessions >= v_max_sessions THEN RETURN; END IF;

    INSERT INTO radius_access_reservations (tenant_id, subscription_id, normalized_mac, expires_at)
    SELECT tenant_id, id, v_mac, p_at + interval '120 seconds'
      FROM subscriptions WHERE id = v_subscription_id;
    UPDATE devices SET last_seen_at = GREATEST(COALESCE(last_seen_at, p_at), p_at), updated_at = now()
      WHERE normalized_mac = v_mac AND id = (SELECT device_id FROM subscriptions WHERE id = v_subscription_id);

    RETURN QUERY SELECT v_subscription_id, policy.rate_limit, policy.session_timeout_seconds,
      policy.idle_timeout_seconds, policy.interim_interval, policy.total_limit,
      policy.total_limit_gigawords, policy.filter_id
      FROM radius_portal_access_policy(v_subscription_id, p_at) AS policy;
    IF NOT FOUND THEN
      DELETE FROM radius_access_reservations WHERE subscription_id = v_subscription_id AND normalized_mac = v_mac;
    END IF;
END;
$$;

REVOKE ALL ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) TO netcore_radius;
COMMIT;
