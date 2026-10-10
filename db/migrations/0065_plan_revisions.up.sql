BEGIN;

-- A referenced plan is immutable. Editing it publishes a replacement row;
-- subscriptions, paid orders and issued vouchers retain the original terms.
ALTER TABLE plans
    ADD COLUMN superseded_by_plan_id uuid REFERENCES plans(id) ON DELETE RESTRICT,
    ADD CONSTRAINT plans_superseded_retired CHECK (superseded_by_plan_id IS NULL OR status = 'RETIRED');

CREATE INDEX plans_superseded_by_idx ON plans(superseded_by_plan_id)
    WHERE superseded_by_plan_id IS NOT NULL;

COMMIT;
