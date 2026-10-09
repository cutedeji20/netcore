BEGIN;
ALTER TABLE tenant_billing_settings DROP COLUMN reseller_discount_basis_points;
COMMIT;
