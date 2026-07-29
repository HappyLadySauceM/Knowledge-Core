CREATE SCHEMA IF NOT EXISTS knowledge;

CREATE TABLE knowledge.documents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug TEXT UNIQUE,
    title VARCHAR(200) NOT NULL,
    summary VARCHAR(1000) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    author_id BIGINT NOT NULL,
    current_version BIGINT NOT NULL DEFAULT 0,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    search_vector TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(summary, ''))
    ) STORED,
    CONSTRAINT documents_status_check CHECK (status IN ('draft', 'published')),
    CONSTRAINT documents_author_id_check CHECK (author_id > 0),
    CONSTRAINT documents_current_version_check CHECK (current_version >= 0)
);

CREATE INDEX documents_published_at_idx ON knowledge.documents (published_at DESC) WHERE status = 'published';
CREATE INDEX documents_search_vector_idx ON knowledge.documents USING GIN (search_vector);

CREATE TABLE knowledge.document_blocks (
    block_id VARCHAR(64) PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    position_key TEXT NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'paragraph',
    content_json JSONB NOT NULL,
    text_content TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    updated_by BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT document_blocks_version_check CHECK (version > 0),
    CONSTRAINT document_blocks_updated_by_check CHECK (updated_by > 0)
);

CREATE INDEX document_blocks_document_position_idx ON knowledge.document_blocks (document_id, position_key, block_id);

CREATE TABLE knowledge.document_ops (
    op_id VARCHAR(64) PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    actor_id BIGINT NOT NULL,
    base_document_version BIGINT NOT NULL,
    block_id VARCHAR(64) NOT NULL,
    op_type VARCHAR(32) NOT NULL,
    payload_json JSONB NOT NULL,
    document_version BIGINT NOT NULL,
    block_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT document_ops_actor_id_check CHECK (actor_id > 0),
    CONSTRAINT document_ops_base_document_version_check CHECK (base_document_version >= 0),
    CONSTRAINT document_ops_document_version_check CHECK (document_version > 0),
    CONSTRAINT document_ops_block_version_check CHECK (block_version > 0)
);

CREATE INDEX document_ops_document_version_idx ON knowledge.document_ops (document_id, document_version);

CREATE TABLE knowledge.document_revisions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    version BIGINT NOT NULL,
    title VARCHAR(200) NOT NULL,
    summary VARCHAR(1000) NOT NULL,
    content_json JSONB NOT NULL,
    published_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT document_revisions_version_check CHECK (version >= 0),
    CONSTRAINT document_revisions_published_by_check CHECK (published_by > 0),
    CONSTRAINT document_revisions_document_version_uidx UNIQUE (document_id, version)
);

CREATE INDEX document_revisions_document_created_idx ON knowledge.document_revisions (document_id, created_at DESC);
