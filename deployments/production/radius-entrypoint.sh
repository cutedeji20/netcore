#!/bin/sh
set -eu

# The upstream Alpine image installs FreeRADIUS under /opt and normally adds
# these directories in its entrypoint. NetCore replaces that entrypoint, so
# preserve the upstream binary path explicitly.
PATH=/opt/sbin:/opt/bin:$PATH
export PATH

password_file=/run/netcore/runtime/radius_db_password
if [ ! -r "$password_file" ]; then
  echo "netcore radius: missing readable database password file" >&2
  exit 64
fi

RADIUS_DB_PASSWORD=$(tr -d '\r\n' < "$password_file")
if [ -z "$RADIUS_DB_PASSWORD" ]; then
  echo "netcore radius: database password file is empty" >&2
  exit 64
fi
case "$RADIUS_DB_PASSWORD" in
  *[!A-Za-z0-9_-]*)
    echo "netcore radius: database password must be a base64url value" >&2
    exit 64
    ;;
esac

: "${RADIUS_DB_HOST:?netcore radius: missing database host}"
: "${RADIUS_DB_PORT:?netcore radius: missing database port}"
: "${RADIUS_DB_NAME:?netcore radius: missing database name}"
: "${RADIUS_DB_USER:?netcore radius: missing database user}"

# libpq reads the password from this process-private file instead of a DSN.
# Keeping it out of radius_db prevents normal FreeRADIUS startup logs from
# disclosing the credential. The base64url validation above guarantees that
# the password cannot inject a pgpass field separator.
PGPASSFILE=/tmp/netcore-radius.pgpass
umask 077
printf '%s:%s:%s:%s:%s\n' \
  "$RADIUS_DB_HOST" \
  "$RADIUS_DB_PORT" \
  "$RADIUS_DB_NAME" \
  "$RADIUS_DB_USER" \
  "$RADIUS_DB_PASSWORD" > "$PGPASSFILE"
chmod 0600 "$PGPASSFILE"
export PGPASSFILE
unset RADIUS_DB_PASSWORD

# MAC auto-login has its own deployment-managed secret. It is read from a
# dedicated runtime file rather than Compose/.env so it cannot leak through
# environment inspection or source control.
mac_auth_password_file=/run/netcore/runtime/mac_auth_password
if [ ! -r "$mac_auth_password_file" ]; then
  echo "netcore radius: missing readable MAC authentication password file" >&2
  exit 64
fi
NETCORE_RADIUS_MAC_AUTH_PASSWORD=$(tr -d '\r\n' < "$mac_auth_password_file")
if [ -z "$NETCORE_RADIUS_MAC_AUTH_PASSWORD" ]; then
  echo "netcore radius: MAC authentication password file is empty" >&2
  exit 64
fi
case "$NETCORE_RADIUS_MAC_AUTH_PASSWORD" in
  *[!A-Za-z0-9_-]*)
    echo "netcore radius: MAC authentication password must be a base64url value" >&2
    exit 64
    ;;
esac
export NETCORE_RADIUS_MAC_AUTH_PASSWORD

mode=${NETCORE_RADIUS_MODE:-writer}
case "$mode" in
  replay)
    config_dir=/opt/netcore-radius/replay
    ;;
  writer)
    config_dir=/opt/netcore-radius/writer
    ;;
  *)
    echo "netcore radius: NETCORE_RADIUS_MODE must be writer or replay" >&2
    exit 64
    ;;
esac

# This lets the documented Compose validation use the same mounted password
# and TLS paths as the running service, instead of bypassing this entrypoint.
if [ -n "${NETCORE_RADIUS_VALIDATE:-}" ]; then
  if [ "$NETCORE_RADIUS_VALIDATE" != "1" ]; then
    echo "netcore radius: NETCORE_RADIUS_VALIDATE must be 1 when set" >&2
    exit 64
  fi
  exec radiusd -d "$config_dir" -C
fi

if [ "$mode" = "replay" ]; then
  exec radiusd -d "$config_dir" -f
fi

# The UDP server and the capacity guard run together. If the durable spool
# reaches its configured ceiling, terminate this writer so NAS devices retry
# rather than receiving an Accounting-Response for data that cannot be kept.
radiusd -d "$config_dir" -f &
radius_pid=$!
/usr/local/bin/netcore-radius-spool-guard &
guard_pid=$!

shutdown() {
  kill -TERM "$radius_pid" "$guard_pid" 2>/dev/null || true
  wait "$radius_pid" 2>/dev/null || true
}
trap shutdown INT TERM

while :; do
  if ! kill -0 "$guard_pid" 2>/dev/null; then
    echo "netcore radius: spool guard stopped; terminating UDP writer" >&2
    kill -TERM "$radius_pid" 2>/dev/null || true
    wait "$radius_pid" 2>/dev/null || true
    exit 42
  fi
  if ! kill -0 "$radius_pid" 2>/dev/null; then
    wait "$radius_pid"
    exit $?
  fi
  sleep 1
done
