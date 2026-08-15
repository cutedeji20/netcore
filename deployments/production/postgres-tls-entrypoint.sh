#!/bin/sh
set -eu

tls_dir=/tls
if [ -s "$tls_dir/ca.crt" ] && [ -s "$tls_dir/server.crt" ] && [ -s "$tls_dir/server.key" ]; then
  exit 0
fi

umask 077
rm -f "$tls_dir/ca.crt" "$tls_dir/ca.key" "$tls_dir/server.crt" "$tls_dir/server.csr" "$tls_dir/server.key"

openssl genrsa -out "$tls_dir/ca.key" 4096
openssl req -x509 -new -nodes -key "$tls_dir/ca.key" -sha256 -days 365 -out "$tls_dir/ca.crt" -subj "/CN=NetCore PostgreSQL CA"
openssl req -new -newkey rsa:4096 -nodes -keyout "$tls_dir/server.key" -out "$tls_dir/server.csr" -subj "/CN=postgres"
printf 'subjectAltName=DNS:postgres\nextendedKeyUsage=serverAuth\n' > "$tls_dir/server.ext"
openssl x509 -req -in "$tls_dir/server.csr" -CA "$tls_dir/ca.crt" -CAkey "$tls_dir/ca.key" -CAcreateserial -out "$tls_dir/server.crt" -days 365 -sha256 -extfile "$tls_dir/server.ext"
rm -f "$tls_dir/server.csr" "$tls_dir/server.ext" "$tls_dir/ca.srl"

# postgres:16-alpine uses the postgres UID 70. The API and migration service
# read only the public CA; the server key is readable only by PostgreSQL.
chown 70:70 "$tls_dir/server.key"
chmod 0600 "$tls_dir/server.key"
chmod 0644 "$tls_dir/ca.crt" "$tls_dir/server.crt"
