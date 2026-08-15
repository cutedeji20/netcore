#!/bin/sh
set -eu

spool=/var/lib/netcore-radius/spool
max_bytes=${NETCORE_RADIUS_SPOOL_MAX_BYTES:-5368709120}
min_free_bytes=${NETCORE_RADIUS_SPOOL_MIN_FREE_BYTES:-1073741824}
interval=${NETCORE_RADIUS_SPOOL_CHECK_INTERVAL_SECONDS:-10}

for value in "$max_bytes" "$min_free_bytes" "$interval"; do
  case "$value" in
  ''|*[!0-9]*)
    echo "netcore radius: spool capacity settings must be positive integers" >&2
    exit 64
    ;;
  esac
done

if [ "$max_bytes" -lt 104857600 ] || [ "$min_free_bytes" -lt 104857600 ] || [ "$interval" -lt 1 ] || [ "$interval" -gt 300 ]; then
  echo "netcore radius: unsafe spool capacity settings" >&2
  exit 64
fi

while :; do
  used_kib=$(du -sk "$spool" | awk '{print $1}')
  free_kib=$(df -Pk "$spool" | awk 'NR == 2 { print $4 }')
  if [ -z "$used_kib" ] || [ -z "$free_kib" ]; then
    echo "event=radius_spool_measurement_failed path=$spool" >&2
    exit 43
  fi
  used_bytes=$((used_kib * 1024))
  free_bytes=$((free_kib * 1024))
  if [ "$used_bytes" -ge "$max_bytes" ] || [ "$free_bytes" -le "$min_free_bytes" ]; then
    echo "event=radius_spool_capacity_exceeded used_bytes=$used_bytes max_bytes=$max_bytes free_bytes=$free_bytes min_free_bytes=$min_free_bytes" >&2
    exit 42
  fi
  sleep "$interval"
done
