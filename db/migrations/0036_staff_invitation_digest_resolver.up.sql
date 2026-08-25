-- Public invitation credentials carry no tenant identifier. This narrowly
-- scoped resolver returns only the tenant UUID for a 32-byte digest, so the
-- application can immediately re-enter its normal forced-RLS transaction.
-- It never returns an invitation row, email, encrypted MFA material, or token.

BEGIN;

CREATE FUNCTION staff_invitation_tenant_for_digest(p_digest bytea)
RETURNS uuid
LANGUAGE sql
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT tenant_id
      FROM staff_invitations
     WHERE token_digest = p_digest
       AND status = 'PENDING'
     LIMIT 1
$$;

REVOKE ALL ON FUNCTION staff_invitation_tenant_for_digest(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION staff_invitation_tenant_for_digest(bytea) TO netcore_app_rw;

COMMIT;
