#!/bin/sh
set -eu

service_file=/run/netcore/runtime/migration_pg_service.conf
if [ ! -r "$service_file" ]; then
  echo "netcore migrate: missing readable migration service file" >&2
  exit 1
fi

export PGSERVICE=netcore-migration
export PGSERVICEFILE="$service_file"

psql -X --set=ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS netcore_schema_migrations (
  version text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);
REVOKE ALL ON netcore_schema_migrations FROM PUBLIC;
SQL

for migration in /migrations/sql/*.up.sql; do
  version=$(basename "$migration" | sed 's/_.*//')
  applied=$(psql -X -At --set=ON_ERROR_STOP=1 --set="version=$version" -c "SELECT EXISTS (SELECT 1 FROM netcore_schema_migrations WHERE version = :'version')")
  if [ "$applied" = "t" ]; then
    continue
  fi
  echo "applying $migration"
  psql -X --set=ON_ERROR_STOP=1 -f "$migration"
  psql -X --set=ON_ERROR_STOP=1 --set="version=$version" -c "INSERT INTO netcore_schema_migrations (version) VALUES (:'version')"
done

# Login roles are created only on a fresh PostgreSQL volume. Grant their
# deliberately narrow NOLOGIN capability roles after every migration run.
psql -X --set=ON_ERROR_STOP=1 -f /migrations/bootstrap_roles.sql
