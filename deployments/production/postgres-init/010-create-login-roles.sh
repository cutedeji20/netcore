#!/bin/sh
set -eu

runtime_dir=/run/netcore/runtime
for secret in postgres_api_password postgres_radius_password; do
  if [ ! -r "$runtime_dir/$secret" ]; then
    echo "netcore postgres init: missing readable $secret" >&2
    exit 1
  fi
done

# The RADIUS password is interpolated into a libpq connection string by a
# non-root service. Restrict it to base64url so its value cannot alter the
# connection syntax. The matching FreeRADIUS-only file is checked there too.
radius_password=$(tr -d '\r\n' < "$runtime_dir/postgres_radius_password")
case "$radius_password" in
  ''|*[!A-Za-z0-9_-]*)
    echo "netcore postgres init: postgres_radius_password must be a non-empty base64url value" >&2
    exit 1
    ;;
esac

# psql reads the values from the mounted files itself. They therefore never
# become Docker environment variables or command-line arguments.
psql --set=ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
\set api_password `cat /run/netcore/runtime/postgres_api_password`
\set radius_password `cat /run/netcore/runtime/postgres_radius_password`

SELECT format('CREATE ROLE netcore_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'api_password')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_api')
\gexec

SELECT format('CREATE ROLE netcore_radius_login LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'radius_password')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'netcore_radius_login')
\gexec

REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- The initial PostgreSQL user is a superuser only long enough to initialise
-- the cluster. It becomes the protected migration/function-owner role before
-- any schema migration creates SECURITY DEFINER functions.
ALTER ROLE netcore_owner NOSUPERUSER NOCREATEDB CREATEROLE NOREPLICATION BYPASSRLS;
SQL
