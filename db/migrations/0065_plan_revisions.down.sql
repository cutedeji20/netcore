BEGIN;
DROP INDEX IF EXISTS plans_superseded_by_idx;
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_superseded_retired;
ALTER TABLE plans DROP COLUMN IF EXISTS superseded_by_plan_id;
COMMIT;
