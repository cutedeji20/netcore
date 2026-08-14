-- 0005_identity_sessions.up.sql
-- Phase 2: browser sessions and complete tenant isolation for identity/RBAC.
--
-- Session tokens are bearer credentials. Only SHA-256 digests are persisted;
-- a database read must not become an authenticated browser session.

BEGIN;

-- The composite key also prevents a session from linking a tenant to a user
-- from another tenant. RLS hides such a mistake; this constraint rejects it.
ALTER TABLE users
    ADD CONSTRAINT users_tenant_id_id_key UNIQUE (tenant_id, id);

CREATE TABLE auth_sessions (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id       uuid        NOT NULL,
    token_hash    bytea       NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at    timestamptz NOT NULL,
    invalidated_at timestamptz,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    ip_address    inet,
    user_agent    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT auth_sessions_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT auth_sessions_user_tenant_fkey
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX auth_sessions_active_user_idx
    ON auth_sessions (tenant_id, user_id, expires_at)
    WHERE invalidated_at IS NULL;

-- Migration 0004 established RLS for business data. Phase 2 brings identity,
-- RBAC links and the tenant-owned audit trail under the same defense. The
-- association tables have no tenant_id, so their policies derive it from the
-- protected user/role rows and apply to both reads and writes.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON users
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE roles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON roles
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

ALTER TABLE user_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_roles
    USING (
        EXISTS (
            SELECT 1 FROM users u
             WHERE u.id = user_roles.user_id
               AND u.tenant_id = current_tenant_id()
        )
    )
    WITH CHECK (
        EXISTS (
            SELECT 1 FROM users u
             WHERE u.id = user_roles.user_id
               AND u.tenant_id = current_tenant_id()
        )
        AND EXISTS (
            SELECT 1 FROM roles r
             WHERE r.id = user_roles.role_id
               AND r.tenant_id = current_tenant_id()
        )
    );

ALTER TABLE role_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE role_permissions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON role_permissions
    USING (
        EXISTS (
            SELECT 1 FROM roles r
             WHERE r.id = role_permissions.role_id
               AND r.tenant_id = current_tenant_id()
        )
    )
    WITH CHECK (
        EXISTS (
            SELECT 1 FROM roles r
             WHERE r.id = role_permissions.role_id
               AND r.tenant_id = current_tenant_id()
        )
    );

ALTER TABLE auth_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON auth_sessions
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_logs
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

-- 0004's blanket grants apply only to tables that existed at the time. New
-- tables need explicit, least-privilege grants in the migration that creates
-- them. Reporting does not need session-token digests.
GRANT SELECT, INSERT, UPDATE, DELETE ON auth_sessions TO netcore_app_rw;

COMMIT;
