BEGIN;

DROP FUNCTION IF EXISTS radius_portal_handoff_authorize(text, inet, text, timestamptz);
DROP TRIGGER IF EXISTS sessions_release_radius_access_reservation ON sessions;
DROP FUNCTION IF EXISTS radius_access_reservation_release();

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

DROP TABLE IF EXISTS radius_access_reservations;

COMMIT;
