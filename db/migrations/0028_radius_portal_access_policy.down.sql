BEGIN;

DROP FUNCTION IF EXISTS radius_portal_handoff_authorize(text, inet, text, timestamptz);
DROP FUNCTION IF EXISTS radius_portal_access_policy(uuid, timestamptz);

-- Restore the pre-0028 grant when rolling this migration back.
GRANT EXECUTE ON FUNCTION radius_portal_handoff_consume(text, inet, text) TO netcore_radius;

COMMIT;
