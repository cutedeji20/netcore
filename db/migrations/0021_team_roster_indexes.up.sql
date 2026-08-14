-- 0021_team_roster_indexes.up.sql
-- Supports tenant roster pagination and last-active lookups.

BEGIN;

CREATE INDEX users_tenant_created_id_idx
    ON users (tenant_id, created_at DESC, id DESC);

CREATE INDEX auth_sessions_tenant_user_seen_idx
    ON auth_sessions (tenant_id, user_id, last_seen_at DESC);

COMMIT;
