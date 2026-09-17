-- Existing deployments already recorded 0043. Recreate its authorization
-- function with qualified table columns: RETURNS TABLE exposes an output
-- variable named subscription_id, which otherwise collides at execution time.

BEGIN;

CREATE OR REPLACE FUNCTION radius_portal_handoff_authorize(
    p_nonce      text,
    p_nas        inet,
    p_client_mac text,
    p_at         timestamptz DEFAULT now()
) RETURNS TABLE (
    subscription_id           uuid,
    rate_limit                text,
    session_timeout_seconds   integer,
    idle_timeout_seconds      integer,
    interim_interval          integer,
    total_limit               bigint,
    total_limit_gigawords     bigint,
    filter_id                 text
)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    v_handoff_id          uuid;
    v_tenant_id           uuid;
    v_subscription_id     uuid;
    v_customer_id         uuid;
    v_mac                 text;
    v_max_devices         integer;
    v_max_sessions        integer;
    v_device_customer_id  uuid;
    v_registered_devices  integer;
    v_active_sessions     integer;
    v_reserved_sessions   integer;
BEGIN
    v_mac := lower(regexp_replace(COALESCE(p_client_mac, ''), '[^0-9A-Fa-f]', '', 'g'));
    IF p_nas IS NULL OR v_mac !~ '^[0-9a-f]{12}$' OR p_nonce !~ '^[A-Za-z0-9_-]{43}$' THEN
        RETURN;
    END IF;

    SELECT handoff.id, handoff.tenant_id, handoff.subscription_id,
           subscription.customer_id, plan.max_devices, plan.max_concurrent_sessions
      INTO v_handoff_id, v_tenant_id, v_subscription_id, v_customer_id,
           v_max_devices, v_max_sessions
      FROM portal_handoffs AS handoff
      JOIN nas ON nas.id = handoff.nas_id
              AND nas.tenant_id = handoff.tenant_id
              AND nas.nasname = p_nas
              AND nas.status = 'ACTIVE'
      JOIN subscriptions AS subscription
        ON subscription.id = handoff.subscription_id
       AND subscription.tenant_id = handoff.tenant_id
       AND subscription.status = 'ACTIVE'
       AND subscription.starts_at <= p_at
       AND subscription.expires_at > p_at
      JOIN plans AS plan ON plan.id = subscription.plan_id
                        AND plan.tenant_id = subscription.tenant_id
                        AND plan.status = 'ACTIVE'
     WHERE handoff.nonce_hash = digest(p_nonce, 'sha256')
       AND handoff.client_mac = v_mac
       AND handoff.consumed_at IS NULL
       AND handoff.expires_at > p_at
     FOR UPDATE OF handoff;
    IF NOT FOUND THEN RETURN; END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(v_subscription_id::text, 0));
    DELETE FROM radius_access_reservations WHERE expires_at <= p_at;

    SELECT count(*) INTO v_active_sessions
      FROM sessions AS session_row
     WHERE session_row.subscription_id = v_subscription_id
       AND session_row.status <> 'CLOSED';
    SELECT count(*) INTO v_reserved_sessions
      FROM radius_access_reservations AS reservation
     WHERE reservation.subscription_id = v_subscription_id
       AND reservation.expires_at > p_at;
    IF v_active_sessions + v_reserved_sessions >= v_max_sessions THEN RETURN; END IF;

    SELECT device_row.customer_id INTO v_device_customer_id
      FROM devices AS device_row
     WHERE device_row.tenant_id = v_tenant_id
       AND device_row.normalized_mac = v_mac
       AND device_row.status = 'ACTIVE'
     FOR UPDATE;
    IF FOUND AND v_device_customer_id <> v_customer_id THEN RETURN; END IF;
    IF NOT FOUND THEN
        SELECT count(*) INTO v_registered_devices
          FROM devices AS device_row
         WHERE device_row.tenant_id = v_tenant_id
           AND device_row.customer_id = v_customer_id
           AND device_row.status = 'ACTIVE';
        IF v_registered_devices >= v_max_devices THEN RETURN; END IF;
        INSERT INTO devices (tenant_id, customer_id, mac_address, normalized_mac, last_seen_at)
        VALUES (v_tenant_id, v_customer_id, v_mac, v_mac, p_at);
    ELSE
        UPDATE devices SET last_seen_at = GREATEST(COALESCE(last_seen_at, p_at), p_at), updated_at = now()
         WHERE devices.tenant_id = v_tenant_id
           AND devices.normalized_mac = v_mac
           AND devices.status = 'ACTIVE';
    END IF;

    INSERT INTO radius_access_reservations (tenant_id, subscription_id, normalized_mac, expires_at)
    VALUES (v_tenant_id, v_subscription_id, v_mac, p_at + interval '120 seconds');
    UPDATE portal_handoffs SET consumed_at = p_at WHERE id = v_handoff_id;

    RETURN QUERY
    SELECT v_subscription_id, policy.rate_limit, policy.session_timeout_seconds,
           policy.idle_timeout_seconds, policy.interim_interval, policy.total_limit,
           policy.total_limit_gigawords, policy.filter_id
      FROM radius_portal_access_policy(v_subscription_id, p_at) AS policy;

    IF NOT FOUND THEN
        DELETE FROM radius_access_reservations
         WHERE radius_access_reservations.subscription_id = v_subscription_id
           AND radius_access_reservations.normalized_mac = v_mac;
    END IF;
END;
$$;

COMMIT;
