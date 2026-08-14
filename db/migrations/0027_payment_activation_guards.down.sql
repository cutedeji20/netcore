BEGIN;

DROP TRIGGER IF EXISTS payments_no_change_after_success ON payments;
DROP FUNCTION IF EXISTS payments_immutable_after_success();

DROP POLICY IF EXISTS tenant_isolation ON idempotency_keys;
ALTER TABLE idempotency_keys DISABLE ROW LEVEL SECURITY;

COMMIT;
