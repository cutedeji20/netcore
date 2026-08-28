BEGIN;

-- Restore the historical catalogue-state check when rolling this migration
-- back. Normal production deployments only apply the forward migration.
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
      ELSE NULL
    END AS filter_id
  FROM normalized
 WHERE NOT (metered AND remaining_bytes = 0
            AND quota_exhausted_action = 'DISCONNECT');
$$;

DELETE FROM role_permissions
 WHERE permission_id = (SELECT id FROM permissions WHERE name = 'plan.delete');
DELETE FROM permissions WHERE name = 'plan.delete';

COMMIT;
