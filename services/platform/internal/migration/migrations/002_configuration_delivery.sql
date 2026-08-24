CREATE TABLE IF NOT EXISTS platform.configuration_revisions (
    environment varchar(32) NOT NULL,
    namespace varchar(32) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    public_values jsonb NOT NULL,
    secret_envelope jsonb,
    updated_by bigint NOT NULL CHECK (updated_by > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (environment, namespace, revision)
);
CREATE INDEX IF NOT EXISTS configuration_revisions_latest_idx
    ON platform.configuration_revisions (environment, namespace, revision DESC);

INSERT INTO platform.configuration_revisions (
    environment, namespace, revision, schema_version, public_values, secret_envelope,
    updated_by, created_at, updated_at
)
SELECT environment, namespace, revision, schema_version, public_values, secret_envelope,
       updated_by, created_at, updated_at
FROM platform.configurations
ON CONFLICT (environment, namespace, revision) DO NOTHING;

CREATE TABLE IF NOT EXISTS platform.config_deliveries (
    environment varchar(32) NOT NULL,
    namespace varchar(32) NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    consumer varchar(64) NOT NULL,
    message_id uuid NOT NULL,
    status varchar(32) NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error_key varchar(128) NOT NULL DEFAULT '',
    applied_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (environment, namespace, revision, consumer)
);
CREATE INDEX IF NOT EXISTS config_deliveries_latest_idx
    ON platform.config_deliveries (environment, namespace, consumer, revision DESC);
