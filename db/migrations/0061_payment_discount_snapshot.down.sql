BEGIN;
ALTER TABLE payments DROP CONSTRAINT payments_discount_snapshot_check;
ALTER TABLE payments
    DROP COLUMN reseller_at_initiation,
    DROP COLUMN reseller_discount_minor,
    DROP COLUMN reseller_discount_basis_points,
    DROP COLUMN base_plan_amount_minor,
    ADD CONSTRAINT payments_amount_breakdown_check CHECK (amount_minor = plan_amount_minor + bank_charge_minor);
COMMIT;
