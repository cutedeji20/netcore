-- 0028_radius_portal_access_policy.up.sql
-- The RADIUS process must never read customer, plan, subscription, or payment
-- tables directly. This narrow SECURITY DEFINER function returns only the
-- reply attributes needed immediately after a portal handoff is consumed.

BEGIN;

CREATE FUNCTION radius_portal_access_policy(
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
       AND p.status = 'ACTIVE'
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

REVOKE ALL ON FUNCTION radius_portal_access_policy(uuid, timestamptz) FROM PUBLIC;

COMMENT ON FUNCTION radius_portal_access_policy IS
'RADIUS-only reply policy after a consumed portal handoff. It derives active '
'entitlement, rate, bounded session timeout, quota budget, and reduced-budget '
'fallback without granting netcore_radius direct access to business tables.';

-- FreeRADIUS must evaluate the handoff only once: the inner function consumes
-- the nonce. This wrapper keeps the authorize virtual server to one SQL call
-- and exposes only the values it may return to the NAS.
CREATE FUNCTION radius_portal_handoff_authorize(
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
LANGUAGE sql SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
    SELECT handoff.subscription_id,
           policy.rate_limit,
           policy.session_timeout_seconds,
           policy.idle_timeout_seconds,
           policy.interim_interval,
           policy.total_limit,
           policy.total_limit_gigawords,
           policy.filter_id
      FROM radius_portal_handoff_consume(p_nonce, p_nas, p_client_mac) AS handoff
      CROSS JOIN LATERAL radius_portal_access_policy(handoff.subscription_id, p_at)
        AS policy;
$$;

REVOKE ALL ON FUNCTION radius_portal_handoff_authorize(text, inet, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION radius_portal_handoff_authorize(text, inet, text, timestamptz) TO netcore_radius;

-- Migration 0026 pre-dated the complete policy wrapper and granted the inner
-- consume function directly. The RADIUS role now receives only the one-call
-- authorize surface, so it cannot obtain a subscription ID without the
-- entitlement and reply-policy evaluation that follows consumption.
REVOKE ALL ON FUNCTION radius_portal_handoff_consume(text, inet, text) FROM netcore_radius;

COMMENT ON FUNCTION radius_portal_handoff_authorize IS
'Atomically consumes a portal nonce and returns the only RADIUS reply policy '
'for the resulting active subscription. A missing row is an Access-Reject.';

COMMIT;
