-- Additive document authoring metadata and explicit history checkpoints.
-- Existing public snapshots remain valid; legacy rows receive an empty hash and
-- are backfilled lazily when they are read or next published.
ALTER TABLE knowledge.documents
    ADD COLUMN IF NOT EXISTS publication_hash varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS icon varchar(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cover_attachment_id uuid,
    ADD COLUMN IF NOT EXISTS cover_focal_x double precision NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS cover_focal_y double precision NOT NULL DEFAULT 50;

ALTER TABLE knowledge.document_publications
    ADD COLUMN IF NOT EXISTS publication_hash varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS icon varchar(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cover_attachment_id uuid,
    ADD COLUMN IF NOT EXISTS cover_focal_x double precision NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS cover_focal_y double precision NOT NULL DEFAULT 50;

ALTER TABLE knowledge.publication_candidates
    ADD COLUMN IF NOT EXISTS publication_hash varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS icon varchar(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cover_attachment_id uuid,
    ADD COLUMN IF NOT EXISTS cover_focal_x double precision NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS cover_focal_y double precision NOT NULL DEFAULT 50;

CREATE TABLE IF NOT EXISTS knowledge.document_commits (
    id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    kind varchar(32) NOT NULL,
    label varchar(160) NOT NULL,
    description varchar(2000) NOT NULL DEFAULT '',
    contributor_id bigint NOT NULL,
    contributor_name varchar(128) NOT NULL,
    sequence bigint NOT NULL,
    content_hash varchar(64) NOT NULL,
    content jsonb NOT NULL,
    plain_text text NOT NULL DEFAULT '',
    is_anchor boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS document_commits_document_created_idx
    ON knowledge.document_commits(document_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS document_commits_document_sequence_idx
    ON knowledge.document_commits(document_id, sequence DESC, id DESC);
