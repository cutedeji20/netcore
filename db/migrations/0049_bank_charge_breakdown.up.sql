BEGIN;

CREATE TABLE tenant_billing_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE RESTRICT,
    fixed_bank_charge_minor bigint NOT NULL DEFAULT 0 CHECK (fixed_bank_charge_minor >= 0),
    currency char(3) NOT NULL DEFAULT 'NGN' CHECK (currency = 'NGN'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE RESTRICT
);

INSERT INTO tenant_billing_settings (tenant_id)
SELECT id FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;

CREATE FUNCTION create_tenant_billing_settings() RETURNS trigger
LANGUAGE plpgsql
SET search_path = public, pg_temp
AS $$
BEGIN
    INSERT INTO tenant_billing_settings (tenant_id) VALUES (NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER tenants_create_billing_settings
    AFTER INSERT ON tenants
    FOR EACH ROW EXECUTE FUNCTION create_tenant_billing_settings();

ALTER TABLE tenant_billing_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_billing_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_billing_settings
    USING (tenant_id = current_tenant_id())
    WITH CHECK (tenant_id = current_tenant_id());

GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_billing_settings TO netcore_app_rw;

ALTER TABLE payments
    ADD COLUMN plan_amount_minor bigint,
    ADD COLUMN bank_charge_minor bigint NOT NULL DEFAULT 0 CHECK (bank_charge_minor >= 0);

UPDATE payments
   SET plan_amount_minor = amount_minor,
       bank_charge_minor = 0
 WHERE plan_amount_minor IS NULL;

ALTER TABLE payments
    ALTER COLUMN plan_amount_minor SET NOT NULL,
    ADD CONSTRAINT payments_amount_breakdown_check
        CHECK (amount_minor = plan_amount_minor + bank_charge_minor);

COMMIT;
