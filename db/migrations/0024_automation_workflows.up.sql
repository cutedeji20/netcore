-- Workflow definitions are tenant-scoped. This initial schema supports the
-- read model only; mutation, scheduling, and execution remain separate
-- audited workflows.

BEGIN;

CREATE TABLE automation_workflows (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           uuid        NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    name                text        NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 160),
    trigger_description text        NOT NULL CHECK (length(trim(trigger_description)) BETWEEN 1 AND 320),
    status              text        NOT NULL DEFAULT 'DRAFT'
                                    CHECK (status IN ('DRAFT','READY','PAUSED')),
    next_run_at         timestamptz,
    owner_id            uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT automation_workflows_owner_tenant_fkey
        FOREIGN KEY (tenant_id, owner_id)
        REFERENCES users (tenant_id, id) ON DELETE SET NULL (owner_id)
);

ALTER TABLE automation_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON automation_workflows
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON automation_workflows TO netcore_app_rw;
GRANT SELECT ON automation_workflows TO netcore_app_ro;

CREATE INDEX automation_workflows_tenant_updated_id_idx
    ON automation_workflows (tenant_id, updated_at DESC, id DESC);

COMMIT;
