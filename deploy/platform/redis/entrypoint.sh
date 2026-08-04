#!/bin/sh
set -eu

: "${REDIS_PASSWORD:?REDIS_PASSWORD is required}"
acl_file=/data/users.acl
if [ ! -s "$acl_file" ]; then
    password_hash="$(printf '%s' "$REDIS_PASSWORD" | sha256sum | awk '{print $1}')"
    umask 077
    printf 'user default on #%s ~* &* +@all\n' "$password_hash" >"$acl_file"
fi
exec redis-server --aclfile "$acl_file" --appendonly yes
