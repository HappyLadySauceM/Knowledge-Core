CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE SCHEMA IF NOT EXISTS knowledge;

CREATE TABLE IF NOT EXISTS knowledge.documents (
    id uuid PRIMARY KEY,
    title varchar(200) NOT NULL,
    summary varchar(1000) NOT NULL DEFAULT '',
    slug varchar(80) NOT NULL,
    owner_id bigint NOT NULL,
    owner_username varchar(32) NOT NULL,
    owner_avatar text NOT NULL DEFAULT '',
    published boolean NOT NULL DEFAULT false,
    metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision > 0),
    content_revision bigint NOT NULL DEFAULT 0 CHECK (content_revision >= 0),
    permission_revision bigint NOT NULL DEFAULT 1 CHECK (permission_revision > 0),
    published_at timestamptz,
    deleted_at timestamptz,
    purge_after timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT documents_title_length_check CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT documents_slug_check CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT documents_publication_check CHECK ((published AND published_at IS NOT NULL) OR (NOT published AND published_at IS NULL)),
    CONSTRAINT documents_deletion_check CHECK ((deleted_at IS NULL AND purge_after IS NULL) OR (deleted_at IS NOT NULL AND purge_after IS NOT NULL AND NOT published))
);

CREATE UNIQUE INDEX IF NOT EXISTS documents_slug_lower_uidx ON knowledge.documents (lower(slug));
CREATE INDEX IF NOT EXISTS documents_public_idx ON knowledge.documents (published_at DESC, id DESC) WHERE published AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS documents_owner_idx ON knowledge.documents (owner_id, updated_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS documents_deleted_idx ON knowledge.documents (owner_id, deleted_at DESC, id DESC) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS documents_search_trgm_idx ON knowledge.documents USING gin ((title || ' ' || summary) gin_trgm_ops);

CREATE TABLE IF NOT EXISTS knowledge.slug_aliases (
    slug varchar(80) PRIMARY KEY,
    document_id uuid REFERENCES knowledge.documents(id) ON DELETE SET NULL,
    gone_at timestamptz,
    created_at timestamptz NOT NULL,
    CONSTRAINT slug_aliases_state_check CHECK ((document_id IS NOT NULL AND gone_at IS NULL) OR (document_id IS NULL AND gone_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS slug_aliases_lower_uidx ON knowledge.slug_aliases (lower(slug));
CREATE INDEX IF NOT EXISTS slug_aliases_document_idx ON knowledge.slug_aliases (document_id) WHERE document_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS knowledge.document_members (
    document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    user_id bigint NOT NULL,
    username varchar(32) NOT NULL,
    avatar text NOT NULL DEFAULT '',
    role varchar(16) NOT NULL CHECK (role IN ('viewer', 'editor')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (document_id, user_id)
);
CREATE INDEX IF NOT EXISTS document_members_user_idx ON knowledge.document_members (user_id, updated_at DESC, document_id);

CREATE TABLE IF NOT EXISTS knowledge.document_projections (
    document_id uuid PRIMARY KEY REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence >= 0),
    content jsonb NOT NULL,
    plain_text text NOT NULL,
    projected_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS document_projections_search_trgm_idx ON knowledge.document_projections USING gin (plain_text gin_trgm_ops);

CREATE TABLE IF NOT EXISTS knowledge.attachments (
    id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    uploader_id bigint NOT NULL,
    filename varchar(255) NOT NULL,
    declared_type varchar(127) NOT NULL,
    detected_type varchar(127) NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    object_key varchar(512) NOT NULL UNIQUE,
    status varchar(32) NOT NULL CHECK (status IN ('pending_upload', 'scanning', 'ready', 'rejected', 'deleting')),
    failure_reason varchar(64) NOT NULL DEFAULT '',
    upload_expires timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_document_idx ON knowledge.attachments (document_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS attachments_uploader_quota_idx ON knowledge.attachments (uploader_id, status);

CREATE TABLE IF NOT EXISTS knowledge.attachment_scan_jobs (
    attachment_id uuid PRIMARY KEY REFERENCES knowledge.attachments(id) ON DELETE CASCADE,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    last_error_key varchar(64) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS attachment_scan_jobs_due_idx ON knowledge.attachment_scan_jobs (next_attempt_at, created_at);

CREATE TABLE IF NOT EXISTS knowledge.outbox (
    id uuid PRIMARY KEY,
    subject varchar(128) NOT NULL,
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    published_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS outbox_pending_idx ON knowledge.outbox (next_attempt_at, created_at) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS knowledge.idempotency_keys (
    actor_id bigint NOT NULL,
    operation varchar(64) NOT NULL,
    key varchar(128) NOT NULL,
    resource_id uuid NOT NULL,
    request_hash char(64) NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (actor_id, operation, key)
);
CREATE INDEX IF NOT EXISTS idempotency_expiry_idx ON knowledge.idempotency_keys (expires_at);
