BEGIN;
DROP TRIGGER tenants_create_billing_settings ON tenants;
DROP FUNCTION create_tenant_billing_settings();
ALTER TABLE payments DROP CONSTRAINT payments_amount_breakdown_check;
ALTER TABLE payments DROP COLUMN bank_charge_minor;
ALTER TABLE payments DROP COLUMN plan_amount_minor;
DROP TABLE tenant_billing_settings;
COMMIT;
