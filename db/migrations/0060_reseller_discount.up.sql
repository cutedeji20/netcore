BEGIN;
ALTER TABLE tenant_billing_settings
    ADD COLUMN reseller_discount_basis_points integer NOT NULL DEFAULT 0
    CONSTRAINT reseller_discount_basis_points_range CHECK (reseller_discount_basis_points BETWEEN 0 AND 9999);
COMMIT;
