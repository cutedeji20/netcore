-- Restore the pre-replacement device-bound RADIUS function.
--
-- Migration 0050 added the nullable subscriptions.device_id binding. This
-- migration teaches radius_portal_handoff_authorize to honor it: a subscription
-- bound to a specific device may authorize ONLY when the connecting
-- Calling-Station-Id resolves to that same active, tenant- and customer-owned
-- devices row. Legacy subscriptions with a NULL device_id keep their prior
-- behavior (find-or-register the MAC under the plan's max_devices limit), so
-- currently active paid users are never silently disconnected.
--
-- Every other authorization invariant is preserved unchanged: ACTIVE NAS match,
-- one-time nonce shape/consumption, subscription and plan status/window,
-- concurrency reservation accounting, and the reply policy produced by
-- radius_portal_access_policy. The function signature is unchanged, so the
-- REVOKE/GRANT established in migration 0028 is preserved by CREATE OR REPLACE;
-- it is re-affirmed below so this migration is self-contained.

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
    v_handoff_id             uuid;
    v_tenant_id              uuid;
    v_subscription_id        uuid;
    v_customer_id            uuid;
    v_subscription_device_id uuid;
    v_mac                    text;
    v_max_devices            integer;
    v_max_sessions           integer;
    v_device_customer_id     uuid;
    v_registered_devices     integer;
    v_active_sessions        integer;
    v_reserved_sessions      integer;
BEGIN
    v_mac := lower(regexp_replace(COALESCE(p_client_mac, ''), '[^0-9A-Fa-f]', '', 'g'));
    IF p_nas IS NULL OR v_mac !~ '^[0-9a-f]{12}$' OR p_nonce !~ '^[A-Za-z0-9_-]{43}$' THEN
        RETURN;
    END IF;

    -- Load the handoff with the same tenant/NAS/subscription/plan guards as
    -- before, additionally capturing the subscription's optional device binding.
    SELECT handoff.id,
           handoff.tenant_id,
           handoff.subscription_id,
           subscription.customer_id,
           subscription.device_id,
           plan.max_devices,
           plan.max_concurrent_sessions
      INTO v_handoff_id,
           v_tenant_id,
           v_subscription_id,
           v_customer_id,
           v_subscription_device_id,
           v_max_devices,
           v_max_sessions
      FROM portal_handoffs AS handoff
      JOIN nas
        ON nas.id = handoff.nas_id
       AND nas.tenant_id = handoff.tenant_id
       AND nas.nasname = p_nas
       AND nas.status = 'ACTIVE'
      JOIN subscriptions AS subscription
        ON subscription.id = handoff.subscription_id
       AND subscription.tenant_id = handoff.tenant_id
       AND subscription.status = 'ACTIVE'
       AND subscription.starts_at <= p_at
       AND subscription.expires_at > p_at
      JOIN plans AS plan
        ON plan.id = subscription.plan_id
       AND plan.tenant_id = subscription.tenant_id
       AND plan.status = 'ACTIVE'
     WHERE handoff.nonce_hash = digest(p_nonce, 'sha256')
       AND handoff.client_mac = v_mac
       AND handoff.consumed_at IS NULL
       AND handoff.expires_at > p_at
     FOR UPDATE OF handoff;

    IF NOT FOUND THEN
        RETURN;
    END IF;

    -- Serialize authorization attempts for one entitlement.  Counting only
    -- sessions is racy because Accounting-Start follows Access-Accept.
    PERFORM pg_advisory_xact_lock(hashtextextended(v_subscription_id::text, 0));
    DELETE FROM radius_access_reservations WHERE expires_at <= p_at;

    SELECT count(*)
      INTO v_active_sessions
      FROM sessions AS session_row
     WHERE session_row.subscription_id = v_subscription_id
       AND session_row.status <> 'CLOSED';
    SELECT count(*)
      INTO v_reserved_sessions
      FROM radius_access_reservations AS reservation
     WHERE reservation.subscription_id = v_subscription_id
       AND reservation.expires_at > p_at;
    IF v_active_sessions + v_reserved_sessions >= v_max_sessions THEN
        RETURN;
    END IF;

    -- A subscription row where subscription.device_id IS NOT NULL is device-bound;
    -- that binding was captured into v_subscription_device_id above.
    IF v_subscription_device_id IS NOT NULL THEN
        -- Device-bound subscription: authorize only the exact device chosen at
        -- checkout, and only when the connecting MAC still matches that active,
        -- customer-owned record.  A different, foreign, or removed device is a
        -- deny; no new device is auto-registered for a bound subscription.
        PERFORM 1
          FROM devices AS device_row
         WHERE device_row.id = v_subscription_device_id
           AND device_row.tenant_id = v_tenant_id
           AND device_row.customer_id = v_customer_id
           AND device_row.normalized_mac = v_mac
           AND device_row.status = 'ACTIVE'
         FOR UPDATE;
        IF NOT FOUND THEN
            RETURN;
        END IF;
        UPDATE devices
           SET last_seen_at = GREATEST(COALESCE(last_seen_at, p_at), p_at),
               updated_at = now()
         WHERE devices.id = v_subscription_device_id
           AND devices.tenant_id = v_tenant_id;
    ELSE
        -- Legacy unbound subscription: unchanged find-or-register behavior under
        -- the plan's max_devices limit, preserving current production access.
        SELECT device_row.customer_id
          INTO v_device_customer_id
          FROM devices AS device_row
         WHERE device_row.tenant_id = v_tenant_id
           AND device_row.normalized_mac = v_mac
           AND device_row.status = 'ACTIVE'
         FOR UPDATE;
        IF FOUND AND v_device_customer_id <> v_customer_id THEN
            -- A MAC already registered to another customer is never reassigned
            -- through an unaudited captive-portal login.
            RETURN;
        END IF;
        IF NOT FOUND THEN
            SELECT count(*)
              INTO v_registered_devices
              FROM devices AS device_row
             WHERE device_row.tenant_id = v_tenant_id
               AND device_row.customer_id = v_customer_id
               AND device_row.status = 'ACTIVE';
            IF v_registered_devices >= v_max_devices THEN
                RETURN;
            END IF;
            INSERT INTO devices (tenant_id, customer_id, mac_address, normalized_mac, last_seen_at)
            VALUES (v_tenant_id, v_customer_id, v_mac, v_mac, p_at);
        ELSE
            UPDATE devices
               SET last_seen_at = GREATEST(COALESCE(last_seen_at, p_at), p_at),
                   updated_at = now()
             WHERE devices.tenant_id = v_tenant_id
               AND devices.normalized_mac = v_mac
               AND devices.status = 'ACTIVE';
        END IF;
    END IF;

    INSERT INTO radius_access_reservations (tenant_id, subscription_id, normalized_mac, expires_at)
    VALUES (v_tenant_id, v_subscription_id, v_mac, p_at + interval '120 seconds');
    UPDATE portal_handoffs SET consumed_at = p_at WHERE id = v_handoff_id;

    RETURN QUERY
    SELECT v_subscription_id,
           policy.rate_limit,
           policy.session_timeout_seconds,
           policy.idle_timeout_seconds,
           policy.interim_interval,
           policy.total_limit,
           policy.total_limit_gigawords,
           policy.filter_id
      FROM radius_portal_access_policy(v_subscription_id, p_at) AS policy;

    -- A policy disappearing between the handoff lookup and return is a deny;
    -- remove the reservation so it cannot temporarily consume a session slot.
    IF NOT FOUND THEN
        DELETE FROM radius_access_reservations
         WHERE radius_access_reservations.subscription_id = v_subscription_id
           AND radius_access_reservations.normalized_mac = v_mac;
    END IF;
END;
$$;

REVOKE ALL ON FUNCTION radius_portal_handoff_authorize(text, inet, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_portal_handoff_authorize(text, inet, text, timestamptz) TO netcore_radius;

COMMENT ON FUNCTION radius_portal_handoff_authorize IS
'Atomically consumes a portal nonce and returns the only RADIUS reply policy '
'for the resulting active subscription. A device-bound subscription authorizes '
'only when Calling-Station-Id matches its bound active device; legacy unbound '
'subscriptions retain find-or-register behavior. A missing row is an Access-Reject.';


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
