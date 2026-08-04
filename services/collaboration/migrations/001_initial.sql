CREATE TABLE collaboration.documents (
    document_id uuid PRIMARY KEY,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    current_sequence bigint NOT NULL DEFAULT 0 CHECK (current_sequence >= 0),
    last_snapshot_sequence bigint NOT NULL DEFAULT 0 CHECK (last_snapshot_sequence >= 0),
    last_version_sequence bigint NOT NULL DEFAULT 0 CHECK (last_version_sequence >= 0),
    last_automatic_version_at timestamptz,
    last_actor_id bigint,
    last_actor_username varchar(32),
    last_actor_avatar text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT documents_watermark_check CHECK (
        last_snapshot_sequence <= current_sequence
        AND last_version_sequence <= current_sequence
    ),
    CONSTRAINT documents_actor_check CHECK (
        (last_actor_id IS NULL AND last_actor_username IS NULL AND last_actor_avatar IS NULL)
        OR (last_actor_id > 0 AND last_actor_username IS NOT NULL AND last_actor_avatar IS NOT NULL)
    )
);

CREATE TABLE collaboration.updates (
    document_id uuid NOT NULL REFERENCES collaboration.documents(document_id) ON DELETE CASCADE,
    generation bigint NOT NULL CHECK (generation > 0),
    sequence bigint NOT NULL CHECK (sequence > 0),
    update bytea NOT NULL CHECK (octet_length(update) > 0),
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, sequence)
);
CREATE INDEX updates_generation_sequence_idx
    ON collaboration.updates (document_id, generation, sequence);

CREATE TABLE collaboration.snapshots (
    document_id uuid NOT NULL REFERENCES collaboration.documents(document_id) ON DELETE CASCADE,
    generation bigint NOT NULL CHECK (generation > 0),
    sequence bigint NOT NULL CHECK (sequence >= 0),
    state bytea NOT NULL CHECK (octet_length(state) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, sequence)
);

CREATE TABLE collaboration.versions (
    id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES collaboration.documents(document_id) ON DELETE CASCADE,
    generation bigint NOT NULL CHECK (generation > 0),
    sequence bigint NOT NULL CHECK (sequence >= 0),
    kind varchar(16) NOT NULL CHECK (kind IN ('manual', 'automatic', 'restoration')),
    label varchar(200),
    state bytea NOT NULL CHECK (octet_length(state) > 0),
    created_by_id bigint NOT NULL CHECK (created_by_id > 0),
    created_by_username varchar(32) NOT NULL,
    created_by_avatar text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX versions_page_idx
    ON collaboration.versions (document_id, created_at DESC, id DESC);

CREATE TABLE collaboration.projection_jobs (
    document_id uuid PRIMARY KEY REFERENCES collaboration.documents(document_id) ON DELETE CASCADE,
    target_generation bigint NOT NULL CHECK (target_generation > 0),
    target_sequence bigint NOT NULL CHECK (target_sequence >= 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    last_error_key varchar(64) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX projection_jobs_due_idx
    ON collaboration.projection_jobs (next_attempt_at, updated_at);

CREATE TABLE collaboration.idempotency_keys (
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    operation varchar(128) NOT NULL,
    key varchar(128) NOT NULL,
    request_hash char(64) NOT NULL,
    resource_id uuid NOT NULL,
    response jsonb NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, operation, key)
);
CREATE INDEX idempotency_expiry_idx
    ON collaboration.idempotency_keys (expires_at);

CREATE TABLE collaboration.outbox (
    id uuid PRIMARY KEY,
    event_key varchar(160) NOT NULL UNIQUE,
    subject varchar(160) NOT NULL,
    document_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    sequence bigint NOT NULL CHECK (sequence >= 0),
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    published_at timestamptz,
    last_error_key varchar(64) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_due_idx
    ON collaboration.outbox (next_attempt_at, created_at)
    WHERE published_at IS NULL;
