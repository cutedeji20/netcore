BEGIN;

DROP INDEX IF EXISTS invoices_tenant_recorded_id_idx;
DROP INDEX IF EXISTS payments_tenant_recorded_id_idx;

COMMIT;
