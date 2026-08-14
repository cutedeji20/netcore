BEGIN;

DROP INDEX IF EXISTS auth_sessions_tenant_user_seen_idx;
DROP INDEX IF EXISTS users_tenant_created_id_idx;

COMMIT;
