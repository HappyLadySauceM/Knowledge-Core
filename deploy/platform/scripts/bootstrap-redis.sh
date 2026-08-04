#!/bin/sh
set -eu

: "${REDIS_ADMIN_PASSWORD:?REDIS_ADMIN_PASSWORD is required}"
: "${KC_TEST_REDIS_PASSWORD:?KC_TEST_REDIS_PASSWORD is required}"
: "${KC_PROD_REDIS_PASSWORD:?KC_PROD_REDIS_PASSWORD is required}"

export REDISCLI_AUTH="$REDIS_ADMIN_PASSWORD"

redis-cli -e -h redis-master.redis.svc.cluster.local ACL SETUSER knowledge-core-test \
    reset on ">${KC_TEST_REDIS_PASSWORD}" '~knowledge-core:test:*' \
    '&knowledge-core.test.*' '+@all' '-@admin' '-flushall' '-flushdb'
redis-cli -e -h redis-master.redis.svc.cluster.local ACL SETUSER knowledge-core-prod \
    reset on ">${KC_PROD_REDIS_PASSWORD}" '~knowledge-core:prod:*' \
    '&knowledge-core.prod.*' '+@all' '-@admin' '-flushall' '-flushdb'

redis-cli -e -h redis-master.redis.svc.cluster.local ACL SAVE
