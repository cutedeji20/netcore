BEGIN;

CREATE OR REPLACE FUNCTION radius_portal_access_policy(
    p_subscription_id uuid,
    p_at              timestamptz DEFAULT now()
) RETURNS TABLE (
    rate_limit              text,
    session_timeout_seconds integer,
    idle_timeout_seconds    integer,
    interim_interval        integer,
    total_limit             bigint,
    total_limit_gigawords   bigint,
    filter_id               text
)
LANGUAGE sql SECURITY DEFINER STABLE
SET search_path = public, pg_temp
AS $$
WITH entitlement AS (
    SELECT s.id,
           s.starts_at,
           s.expires_at,
           p.download_bps,
           p.upload_bps,
           p.throttle_download_bps,
           p.throttle_upload_bps,
           p.quota_bytes IS NOT NULL AS metered,
           p.quota_exhausted_action,
           p.session_timeout_seconds,
           p.idle_timeout_seconds,
           quota.id IS NOT NULL AS quota_known,
           CASE
             WHEN p.quota_bytes IS NULL THEN NULL
             -- An unavailable current counter must not become unlimited
             -- access. The documented reduced, one-session allowance is 256
             -- MiB with a 60-second interim interval.
             WHEN quota.id IS NULL THEN 268435456::bigint
             ELSE GREATEST(0::bigint, p.quota_bytes - quota.consumed_bytes)
           END AS remaining_bytes
      FROM subscriptions AS s
      JOIN plans AS p
        ON p.id = s.plan_id
       AND p.tenant_id = s.tenant_id
      LEFT JOIN LATERAL (
          SELECT uc.id, uc.consumed_bytes
            FROM usage_counters AS uc
           WHERE uc.subscription_id = s.id
             AND uc.period_start <= p_at
             AND uc.period_end > p_at
           ORDER BY uc.period_start DESC
           LIMIT 1
      ) AS quota ON true
     WHERE s.id = p_subscription_id
       AND s.status = 'ACTIVE'
       AND s.starts_at <= p_at
       AND s.expires_at > p_at
       AND p.status IN ('ACTIVE','RETIRED')
), budget AS (
    SELECT *,
           CASE
             WHEN remaining_bytes IS NULL OR remaining_bytes = 0 THEN NULL
             WHEN NOT quota_known THEN 268435456::bigint
             WHEN remaining_bytes <= 67108864 THEN remaining_bytes
             ELSE LEAST(
                 remaining_bytes,
                 GREATEST(67108864::bigint,
                          LEAST(2147483648::bigint, remaining_bytes / 4))
             )
           END AS raw_budget
      FROM entitlement
), normalized AS (
    SELECT *,
           CASE
             -- RouterOS has ambiguous behaviour when a byte budget is an
             -- exact multiple of 2^32 and the low word is zero. Never emit
             -- that shape; one byte less is the safe, bounded choice.
             WHEN raw_budget IS NOT NULL
                  AND raw_budget % 4294967296::bigint = 0
             THEN raw_budget - 1
             ELSE raw_budget
           END AS session_budget
      FROM budget
)
SELECT
    CASE
      WHEN metered AND remaining_bytes = 0
           AND quota_exhausted_action = 'THROTTLE'
      THEN throttle_upload_bps::text || '/' || throttle_download_bps::text
      ELSE upload_bps::text || '/' || download_bps::text
    END AS rate_limit,
    LEAST(
        2147483647::bigint,
        session_timeout_seconds,
        GREATEST(1::bigint, FLOOR(EXTRACT(EPOCH FROM (expires_at - p_at)))::bigint)
    )::integer AS session_timeout_seconds,
    LEAST(2147483647::bigint, idle_timeout_seconds)::integer AS idle_timeout_seconds,
    CASE
      WHEN metered AND (remaining_bytes = 0 OR NOT quota_known
                        OR remaining_bytes < (300::bigint * download_bps / 8))
      THEN 60
      ELSE 300
    END AS interim_interval,
    CASE WHEN session_budget IS NULL THEN NULL
         ELSE session_budget & 4294967295::bigint END AS total_limit,
    CASE WHEN session_budget IS NULL THEN NULL
         ELSE session_budget >> 32 END AS total_limit_gigawords,
    CASE
      WHEN metered AND remaining_bytes = 0
           AND quota_exhausted_action = 'REDIRECT'
      THEN 'netcore-quota-exhausted'
      -- A Filter-Id creates a dynamic RouterOS firewall jump. Do not emit a
      -- dummy/default chain name for normal access; only the explicit
      -- REDIRECT policy is allowed to select a pre-provisioned chain.
      ELSE NULL
    END AS filter_id
  FROM normalized
 -- A plan that explicitly disconnects on exhausted quota receives no policy
 -- row. The RADIUS virtual server treats that as Access-Reject.
 WHERE NOT (metered AND remaining_bytes = 0
            AND quota_exhausted_action = 'DISCONNECT');
$$;

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
       AND plan.status IN ('ACTIVE','RETIRED')
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
               AND plan.tenant_id = candidate.tenant_id AND plan.status IN ('ACTIVE','RETIRED')
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

COMMIT;
