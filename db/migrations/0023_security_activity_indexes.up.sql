-- Keyset pagination for the tenant-scoped immutable audit feed.

BEGIN;

CREATE INDEX audit_logs_tenant_created_id_idx
    ON audit_logs (tenant_id, created_at DESC, id DESC);

COMMIT;
