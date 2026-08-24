CREATE SCHEMA IF NOT EXISTS platform;

CREATE TABLE IF NOT EXISTS platform.configurations (
    environment varchar(32) NOT NULL,
    namespace varchar(32) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    public_values jsonb NOT NULL,
    secret_envelope jsonb,
    updated_by bigint NOT NULL CHECK (updated_by > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (environment, namespace),
    CHECK (namespace IN ('site', 'email', 'ai'))
);

CREATE TABLE IF NOT EXISTS platform.config_audit (
    id uuid PRIMARY KEY,
    environment varchar(32) NOT NULL,
    namespace varchar(32) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    previous_digest char(64) NOT NULL,
    next_digest char(64) NOT NULL,
    changed_keys jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (environment, namespace, revision)
);
CREATE INDEX IF NOT EXISTS config_audit_lookup_idx ON platform.config_audit (environment, namespace, revision DESC);

CREATE TABLE IF NOT EXISTS platform.config_idempotency (
    environment varchar(32) NOT NULL,
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    namespace varchar(32) NOT NULL,
    key varchar(128) NOT NULL,
    request_hash char(64) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    response_public_values jsonb NOT NULL,
    response_secret_keys jsonb NOT NULL,
    response_updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (environment, actor_id, namespace, key)
);
CREATE INDEX IF NOT EXISTS config_idempotency_expiry_idx ON platform.config_idempotency (expires_at);

CREATE TABLE IF NOT EXISTS platform.config_outbox (
    id uuid PRIMARY KEY,
    environment varchar(32) NOT NULL,
    namespace varchar(32) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    subject varchar(128) NOT NULL,
    payload jsonb NOT NULL,
    trace_headers jsonb NOT NULL DEFAULT '{}',
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    last_error_key varchar(64) NOT NULL DEFAULT '',
    parked_at timestamptz,
    published_at timestamptz,
    created_at timestamptz NOT NULL,
    UNIQUE (environment, namespace, revision)
);
CREATE INDEX IF NOT EXISTS config_outbox_pending_idx ON platform.config_outbox (next_attempt_at, created_at, id) WHERE published_at IS NULL AND parked_at IS NULL;
