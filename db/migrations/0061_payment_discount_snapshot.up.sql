BEGIN;
ALTER TABLE payments DROP CONSTRAINT payments_amount_breakdown_check;
ALTER TABLE payments
    ADD COLUMN base_plan_amount_minor bigint,
    ADD COLUMN reseller_discount_basis_points integer NOT NULL DEFAULT 0,
    ADD COLUMN reseller_discount_minor bigint NOT NULL DEFAULT 0,
    ADD COLUMN reseller_at_initiation boolean NOT NULL DEFAULT false;

ALTER TABLE payments DISABLE TRIGGER payments_no_change_after_success;
UPDATE payments SET base_plan_amount_minor = plan_amount_minor WHERE base_plan_amount_minor IS NULL;
ALTER TABLE payments ENABLE TRIGGER payments_no_change_after_success;

ALTER TABLE payments
    ALTER COLUMN base_plan_amount_minor SET NOT NULL,
    ADD CONSTRAINT payments_discount_snapshot_check CHECK (
        base_plan_amount_minor >= 0
        AND plan_amount_minor >= 0
        AND reseller_discount_minor >= 0
        AND reseller_discount_basis_points BETWEEN 0 AND 9999
        AND base_plan_amount_minor = plan_amount_minor + reseller_discount_minor
        AND amount_minor = plan_amount_minor + bank_charge_minor
        AND (reseller_at_initiation OR (reseller_discount_basis_points = 0 AND reseller_discount_minor = 0))
    );
COMMIT;
