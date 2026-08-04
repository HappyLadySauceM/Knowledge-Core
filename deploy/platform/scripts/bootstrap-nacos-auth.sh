#!/bin/sh
set -eu

: "${PGHOST:?PGHOST is required}"
: "${PGUSER:?PGUSER is required}"
: "${PGPASSWORD:?PGPASSWORD is required}"
: "${NACOS_ADMIN_PASSWORD:?NACOS_ADMIN_PASSWORD is required}"
: "${NACOS_TEST_GATEWAY_PASSWORD:?NACOS_TEST_GATEWAY_PASSWORD is required}"
: "${NACOS_TEST_IDENTITY_PASSWORD:?NACOS_TEST_IDENTITY_PASSWORD is required}"
: "${NACOS_TEST_KNOWLEDGE_PASSWORD:?NACOS_TEST_KNOWLEDGE_PASSWORD is required}"
: "${NACOS_TEST_COLLABORATION_PASSWORD:?NACOS_TEST_COLLABORATION_PASSWORD is required}"
: "${NACOS_PROD_GATEWAY_PASSWORD:?NACOS_PROD_GATEWAY_PASSWORD is required}"
: "${NACOS_PROD_IDENTITY_PASSWORD:?NACOS_PROD_IDENTITY_PASSWORD is required}"
: "${NACOS_PROD_KNOWLEDGE_PASSWORD:?NACOS_PROD_KNOWLEDGE_PASSWORD is required}"
: "${NACOS_PROD_COLLABORATION_PASSWORD:?NACOS_PROD_COLLABORATION_PASSWORD is required}"

export PGDATABASE=nacos

attempt=1
while [ "$attempt" -le 60 ]; do
    ready="$(psql --tuples-only --no-align --set=ON_ERROR_STOP=1 \
        --command="SELECT to_regclass('public.users') IS NOT NULL AND EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgcrypto')" \
        2>/dev/null || true)"
    [ "$ready" = "t" ] && break
    [ "$attempt" -lt 60 ] || {
        printf '%s\n' 'Nacos PostgreSQL schema did not become ready before the deadline' >&2
        exit 1
    }
    attempt=$((attempt + 1))
    sleep 2
done

psql --set=ON_ERROR_STOP=1 --set=password="$NACOS_ADMIN_PASSWORD" <<'SQL'
UPDATE users
SET password = crypt(:'password', gen_salt('bf', 10)), enabled = true
WHERE username = 'nacos';
INSERT INTO users (username, password, enabled)
SELECT 'nacos', crypt(:'password', gen_salt('bf', 10)), true
WHERE NOT EXISTS (SELECT 1 FROM users WHERE username = 'nacos');
INSERT INTO roles (username, role)
VALUES ('nacos', 'ROLE_ADMIN')
ON CONFLICT (username, role) DO NOTHING;

INSERT INTO tenant_info (kp, tenant_id, tenant_name, tenant_desc, create_source)
VALUES
    ('1', 'test', 'Knowledge Core test', 'Knowledge Core test runtime configuration', 'bootstrap'),
    ('1', 'prod', 'Knowledge Core prod', 'Knowledge Core production runtime configuration', 'bootstrap')
ON CONFLICT (kp, tenant_id) DO UPDATE
SET tenant_name = EXCLUDED.tenant_name,
    tenant_desc = EXCLUDED.tenant_desc,
    gmt_modified = CURRENT_TIMESTAMP;
SQL

seed_reader() {
    environment="$1"
    service="$2"
    password="$3"
    username="knowledge-core-${environment}-${service}"
    role="${username}-reader"
    resource="${environment}:KNOWLEDGE_CORE:${service}.dynamic.yaml"

    psql --set=ON_ERROR_STOP=1 \
        --set=username="$username" \
        --set=password="$password" \
        --set=role="$role" \
        --set=resource="$resource" <<'SQL'
UPDATE users
SET password = crypt(:'password', gen_salt('bf', 10)), enabled = true
WHERE username = :'username';
INSERT INTO users (username, password, enabled)
SELECT :'username', crypt(:'password', gen_salt('bf', 10)), true
WHERE NOT EXISTS (SELECT 1 FROM users WHERE username = :'username');
INSERT INTO roles (username, role)
VALUES (:'username', :'role')
ON CONFLICT (username, role) DO NOTHING;
INSERT INTO permissions (role, resource, action)
VALUES (:'role', :'resource', 'r')
ON CONFLICT (role, resource, action) DO NOTHING;
SQL
}

seed_reader test gateway "$NACOS_TEST_GATEWAY_PASSWORD"
seed_reader test identity "$NACOS_TEST_IDENTITY_PASSWORD"
seed_reader test knowledge "$NACOS_TEST_KNOWLEDGE_PASSWORD"
seed_reader test collaboration "$NACOS_TEST_COLLABORATION_PASSWORD"
seed_reader prod gateway "$NACOS_PROD_GATEWAY_PASSWORD"
seed_reader prod identity "$NACOS_PROD_IDENTITY_PASSWORD"
seed_reader prod knowledge "$NACOS_PROD_KNOWLEDGE_PASSWORD"
seed_reader prod collaboration "$NACOS_PROD_COLLABORATION_PASSWORD"
