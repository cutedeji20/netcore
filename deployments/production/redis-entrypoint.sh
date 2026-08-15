#!/bin/sh
set -eu

secret_file=/run/netcore/runtime/redis_password
if [ ! -r "$secret_file" ]; then
  echo "netcore redis: missing readable password file" >&2
  exit 1
fi

password=$(tr -d '\r\n' < "$secret_file")
case "$password" in
  ''|*[!A-Za-z0-9_-]*)
    echo "netcore redis: password must be a non-empty base64url value" >&2
    exit 1
    ;;
esac

umask 077
mkdir -p /run/redis
chown redis:redis /data /run/redis
cat > /run/redis/netcore.conf <<EOF
appendonly yes
save 60 1
requirepass $password
EOF
chown redis:redis /run/redis/netcore.conf

exec su -s /bin/sh redis -c 'exec redis-server /run/redis/netcore.conf'
