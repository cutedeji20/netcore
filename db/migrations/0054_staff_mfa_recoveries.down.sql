BEGIN;
DROP FUNCTION IF EXISTS staff_mfa_recovery_tenant_for_digest(bytea);
DROP POLICY IF EXISTS tenant_isolation ON staff_mfa_recoveries;
DROP TABLE IF EXISTS staff_mfa_recoveries;
COMMIT;
