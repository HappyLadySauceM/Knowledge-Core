#!/bin/sh
set -eu

: "${MINIO_ROOT_USER:?MINIO_ROOT_USER is required}"
: "${MINIO_ROOT_PASSWORD:?MINIO_ROOT_PASSWORD is required}"
: "${KC_TEST_MINIO_ACCESS_KEY:?KC_TEST_MINIO_ACCESS_KEY is required}"
: "${KC_TEST_MINIO_SECRET_KEY:?KC_TEST_MINIO_SECRET_KEY is required}"
: "${KC_PROD_MINIO_ACCESS_KEY:?KC_PROD_MINIO_ACCESS_KEY is required}"
: "${KC_PROD_MINIO_SECRET_KEY:?KC_PROD_MINIO_SECRET_KEY is required}"

mc alias set platform http://knowledge-core-minio.knowledge-core-platform.svc.cluster.local:9000 \
    "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null

for environment in test prod; do
    bucket="knowledge-core-${environment}"
    mc mb --ignore-existing "platform/${bucket}" >/dev/null
done

mc admin user add platform "$KC_TEST_MINIO_ACCESS_KEY" "$KC_TEST_MINIO_SECRET_KEY" >/dev/null 2>&1 || \
    mc admin user enable platform "$KC_TEST_MINIO_ACCESS_KEY" >/dev/null
mc admin user add platform "$KC_PROD_MINIO_ACCESS_KEY" "$KC_PROD_MINIO_SECRET_KEY" >/dev/null 2>&1 || \
    mc admin user enable platform "$KC_PROD_MINIO_ACCESS_KEY" >/dev/null

mc admin policy create platform knowledge-core-test /policies/test-minio-policy.json >/dev/null 2>&1 || \
    mc admin policy info platform knowledge-core-test >/dev/null
mc admin policy create platform knowledge-core-prod /policies/prod-minio-policy.json >/dev/null 2>&1 || \
    mc admin policy info platform knowledge-core-prod >/dev/null
mc admin policy attach platform knowledge-core-test --user "$KC_TEST_MINIO_ACCESS_KEY" >/dev/null
mc admin policy attach platform knowledge-core-prod --user "$KC_PROD_MINIO_ACCESS_KEY" >/dev/null
