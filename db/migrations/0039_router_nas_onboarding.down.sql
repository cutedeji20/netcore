BEGIN;

-- A rollback must not discard encrypted material. Remove it only when no
-- router secret has been created, while preserving every router and NAS row.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM router_radius_credentials) THEN
        RAISE EXCEPTION 'cannot roll back router NAS onboarding while encrypted RADIUS credentials exist';
    END IF;
END
$$;

DROP POLICY IF EXISTS tenant_isolation ON router_radius_credentials;
ALTER TABLE IF EXISTS router_radius_credentials DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS router_radius_credentials;
ALTER TABLE nas DROP COLUMN radius_source_ip;

COMMIT;
