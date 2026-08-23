DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_api') THEN
        RAISE EXCEPTION 'netcore_api login role is missing; initialise PostgreSQL with the production init script';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_radius_login') THEN
        RAISE EXCEPTION 'netcore_radius_login role is missing; initialise PostgreSQL with the production init script';
    END IF;
END
$$;

GRANT netcore_app_rw TO netcore_api;
GRANT netcore_radius TO netcore_radius_login;
