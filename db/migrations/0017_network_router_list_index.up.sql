-- 0017_network_router_list_index.up.sql
-- Supports the per-router RADIUS configuration lookup used by the inventory.

BEGIN;

CREATE INDEX nas_tenant_router_idx
    ON nas (tenant_id, router_id);

COMMIT;
