#!/bin/sh
set -eu

: "${PGHOST:?PGHOST is required}"
: "${PGUSER:?PGUSER is required}"
: "${PGPASSWORD:?PGPASSWORD is required}"
: "${KC_TEST_IDENTITY_PASSWORD:?KC_TEST_IDENTITY_PASSWORD is required}"
: "${KC_TEST_KNOWLEDGE_PASSWORD:?KC_TEST_KNOWLEDGE_PASSWORD is required}"
: "${KC_TEST_COLLABORATION_PASSWORD:?KC_TEST_COLLABORATION_PASSWORD is required}"
: "${KC_PROD_IDENTITY_PASSWORD:?KC_PROD_IDENTITY_PASSWORD is required}"
: "${KC_PROD_KNOWLEDGE_PASSWORD:?KC_PROD_KNOWLEDGE_PASSWORD is required}"
: "${KC_PROD_COLLABORATION_PASSWORD:?KC_PROD_COLLABORATION_PASSWORD is required}"
: "${NACOS_POSTGRES_PASSWORD:?NACOS_POSTGRES_PASSWORD is required}"

export PGDATABASE=registry

ensure_owner() {
    role="$1"
    psql --set=ON_ERROR_STOP=1 --set=role="$role" <<'SQL'
SELECT format('CREATE ROLE %I NOLOGIN', :'role')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role') \gexec
SQL
}

ensure_login() {
    role="$1"
    password="$2"
    psql --set=ON_ERROR_STOP=1 --set=role="$role" --set=password="$password" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role', :'password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'role', :'password') \gexec
SQL
}

ensure_database() {
    database="$1"
    owner="$2"
    psql --set=ON_ERROR_STOP=1 --set=database="$database" --set=owner="$owner" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'database', :'owner')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'database') \gexec
SQL
}

ensure_schema() {
    database="$1"
    schema="$2"
    owner="$3"
    psql --dbname="$database" --set=ON_ERROR_STOP=1 \
        --set=schema="$schema" --set=owner="$owner" <<'SQL'
SELECT format('CREATE SCHEMA IF NOT EXISTS %I AUTHORIZATION %I', :'schema', :'owner') \gexec
SELECT format('ALTER SCHEMA %I OWNER TO %I', :'schema', :'owner') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'owner') \gexec
SQL
}

bootstrap_environment() {
    environment="$1"
    identity_password="$2"
    knowledge_password="$3"
    collaboration_password="$4"
    database="knowledge_core_${environment}"
    database_owner="knowledge_core_${environment}_owner"

    ensure_owner "$database_owner"
    ensure_login "knowledge_core_${environment}_identity" "$identity_password"
    ensure_login "knowledge_core_${environment}_knowledge" "$knowledge_password"
    ensure_login "knowledge_core_${environment}_collaboration" "$collaboration_password"
    ensure_database "$database" "$database_owner"
    ensure_schema "$database" identity "knowledge_core_${environment}_identity"
    ensure_schema "$database" knowledge "knowledge_core_${environment}_knowledge"
    ensure_schema "$database" collaboration "knowledge_core_${environment}_collaboration"
}

bootstrap_environment test \
    "$KC_TEST_IDENTITY_PASSWORD" \
    "$KC_TEST_KNOWLEDGE_PASSWORD" \
    "$KC_TEST_COLLABORATION_PASSWORD"
bootstrap_environment prod \
    "$KC_PROD_IDENTITY_PASSWORD" \
    "$KC_PROD_KNOWLEDGE_PASSWORD" \
    "$KC_PROD_COLLABORATION_PASSWORD"

ensure_login nacos "$NACOS_POSTGRES_PASSWORD"
ensure_database nacos nacos
if [ "$(psql --dbname=nacos --tuples-only --no-align --command="SELECT to_regclass('public.config_info') IS NOT NULL")" != "t" ]; then
    psql --dbname=nacos --set=ON_ERROR_STOP=1 --file=/schema/nacos-postgresql.sql
fi
psql --dbname=nacos --set=ON_ERROR_STOP=1 <<'SQL'
CREATE EXTENSION IF NOT EXISTS pgcrypto;
GRANT CONNECT ON DATABASE nacos TO nacos;
GRANT USAGE ON SCHEMA public TO nacos;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO nacos;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO nacos;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO nacos;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO nacos;
SQL
