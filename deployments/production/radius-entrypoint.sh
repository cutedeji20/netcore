#!/bin/sh
set -eu

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
export RADIUS_DB_PASSWORD

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
  exec radiusd -d "$config_dir" -XC
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
