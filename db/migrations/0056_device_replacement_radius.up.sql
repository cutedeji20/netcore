-- Verified pending device replacement; original device-bound authorization retained.
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
    v_pending_id            uuid;
    v_target_device_id      uuid;
    v_pending_user_id       uuid;
    v_policy                record;
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
     FOR UPDATE OF handoff, subscription;

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

    -- Resolve the complete reply before consuming the handoff or changing a
    -- device binding. A missing policy must never leave a transferred plan.
    SELECT * INTO v_policy FROM radius_portal_access_policy(v_subscription_id, p_at);
    IF NOT FOUND THEN
        RETURN;
    END IF;

    -- Only a verified pending request can swap the binding. The MAC and NAS
    -- here are FreeRADIUS observations, not browser assertions. A failed
    -- policy check or any exception rolls back the entire function call.
    IF v_subscription_device_id IS NOT NULL AND v_active_sessions = 0 AND v_reserved_sessions = 0 THEN
        SELECT replacement.id, replacement.target_device_id, replacement.user_id
          INTO v_pending_id, v_target_device_id, v_pending_user_id
          FROM device_replacements AS replacement
         WHERE replacement.tenant_id = v_tenant_id
           AND replacement.subscription_id = v_subscription_id
           AND replacement.old_device_id = v_subscription_device_id
           AND replacement.nas_id = (SELECT id FROM nas WHERE nasname = p_nas AND tenant_id = v_tenant_id AND status = 'ACTIVE' LIMIT 1)
           AND replacement.target_mac = v_mac
           AND replacement.status = 'PENDING'
           AND replacement.expires_at > p_at
         FOR UPDATE;
        IF FOUND THEN
            -- Accounting and authorization must see one quota state. Without
            -- this lock, usage could become exhausted after the precheck but
            -- before the final RADIUS policy is produced.
            PERFORM 1 FROM usage_counters AS quota
             WHERE quota.tenant_id = v_tenant_id
               AND quota.subscription_id = v_subscription_id
               AND quota.period_start <= p_at AND quota.period_end > p_at
             FOR UPDATE;
            PERFORM 1
              FROM subscriptions AS candidate
              JOIN customers AS owner ON owner.id = candidate.customer_id
               AND owner.tenant_id = candidate.tenant_id AND owner.status = 'ACTIVE'
              JOIN plans AS plan ON plan.id = candidate.plan_id
               AND plan.tenant_id = candidate.tenant_id AND plan.status = 'ACTIVE'
              JOIN devices AS old ON old.id = candidate.device_id
               AND old.tenant_id = candidate.tenant_id AND old.customer_id = candidate.customer_id
               AND old.status = 'ACTIVE'
             WHERE candidate.id = v_subscription_id AND candidate.tenant_id = v_tenant_id
               AND candidate.status = 'ACTIVE' AND candidate.starts_at <= p_at AND candidate.expires_at > p_at
               AND (plan.quota_bytes IS NULL OR EXISTS (
                   SELECT 1 FROM usage_counters AS quota
                    WHERE quota.tenant_id = candidate.tenant_id
                      AND quota.subscription_id = candidate.id
                      AND quota.period_start <= p_at AND quota.period_end > p_at
                      AND quota.exhausted_at IS NULL AND quota.consumed_bytes < quota.quota_bytes
               ));
            IF NOT FOUND THEN RETURN; END IF;
            PERFORM 1 FROM devices AS target
             WHERE target.id = v_target_device_id
               AND target.tenant_id = v_tenant_id
               AND target.customer_id = v_customer_id
               AND target.normalized_mac = v_mac
               AND target.status = 'ACTIVE'
             FOR UPDATE;
            IF NOT FOUND THEN RETURN; END IF;
            UPDATE subscriptions SET device_id = v_target_device_id, updated_at = now()
             WHERE id = v_subscription_id AND tenant_id = v_tenant_id AND device_id = v_subscription_device_id;
            IF NOT FOUND THEN RETURN; END IF;
            UPDATE device_replacements SET status = 'CONSUMED', consumed_at = p_at
             WHERE id = v_pending_id AND status = 'PENDING';
            INSERT INTO subscription_events
                (tenant_id, subscription_id, from_status, to_status, reason, actor_type, actor_id, metadata)
            VALUES (v_tenant_id, v_subscription_id, 'ACTIVE', 'ACTIVE',
                    'CUSTOMER_DEVICE_REPLACEMENT', 'CUSTOMER', v_pending_user_id,
                    jsonb_build_object('old_device_id', v_subscription_device_id,
                                       'new_device_id', v_target_device_id));
            INSERT INTO audit_logs
                (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata)
            VALUES (v_tenant_id, 'USER', v_pending_user_id, 'SUBSCRIPTION_DEVICE_REPLACED',
                    'subscription', v_subscription_id,
                    jsonb_build_object('old_device_id', v_subscription_device_id,
                                       'new_device_id', v_target_device_id));
            v_subscription_device_id := v_target_device_id;
        END IF;
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

    RETURN QUERY SELECT v_subscription_id,
                        v_policy.rate_limit,
                        v_policy.session_timeout_seconds,
                        v_policy.idle_timeout_seconds,
                        v_policy.interim_interval,
                        v_policy.total_limit,
                        v_policy.total_limit_gigawords,
                        v_policy.filter_id;
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
    v_policy record;
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
      -- A customer may have more than one active entitlement on this MAC.
      -- Skip an exhausted DISCONNECT plan instead of letting it hide another
      -- eligible plan. Retired plans may still serve existing subscriptions.
      JOIN LATERAL radius_portal_access_policy(subscription.id, p_at) AS eligible_policy ON true
     WHERE nas.nasname = p_nas AND nas.status = 'ACTIVE'
     ORDER BY subscription.expires_at DESC
     LIMIT 1
     FOR UPDATE OF device_row, subscription;
    IF NOT FOUND THEN RETURN; END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(v_subscription_id::text, 0));
    -- Serialize with accounting before resolving the final policy and
    -- granting a session reservation.
    PERFORM 1 FROM usage_counters AS quota
     WHERE quota.subscription_id = v_subscription_id
       AND quota.period_start <= p_at AND quota.period_end > p_at
     FOR UPDATE;
    SELECT * INTO v_policy FROM radius_portal_access_policy(v_subscription_id, p_at);
    IF NOT FOUND THEN RETURN; END IF;
    DELETE FROM radius_access_reservations WHERE expires_at <= p_at;
    SELECT count(*) INTO v_active_sessions FROM sessions AS active_session
     WHERE active_session.subscription_id = v_subscription_id AND active_session.status <> 'CLOSED';
    SELECT count(*) INTO v_reserved_sessions FROM radius_access_reservations AS reservation
     WHERE reservation.subscription_id = v_subscription_id AND reservation.expires_at > p_at;
    IF v_active_sessions + v_reserved_sessions >= v_max_sessions THEN RETURN; END IF;

    INSERT INTO radius_access_reservations (tenant_id, subscription_id, normalized_mac, expires_at)
    SELECT tenant_id, id, v_mac, p_at + interval '120 seconds'
      FROM subscriptions WHERE id = v_subscription_id;
    UPDATE devices SET last_seen_at = GREATEST(COALESCE(last_seen_at, p_at), p_at), updated_at = now()
      WHERE normalized_mac = v_mac AND id = (SELECT device_id FROM subscriptions WHERE id = v_subscription_id);

    RETURN QUERY SELECT v_subscription_id, v_policy.rate_limit, v_policy.session_timeout_seconds,
      v_policy.idle_timeout_seconds, v_policy.interim_interval, v_policy.total_limit,
      v_policy.total_limit_gigawords, v_policy.filter_id;
END;
$$;

REVOKE ALL ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_device_mac_authorize(inet, text, timestamptz) TO netcore_radius;

COMMIT;
