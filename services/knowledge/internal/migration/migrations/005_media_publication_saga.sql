DROP TABLE IF EXISTS knowledge.publication_attachments;
DROP TABLE IF EXISTS knowledge.attachment_scan_jobs;
DROP TABLE IF EXISTS knowledge.attachments;

ALTER TABLE knowledge.documents
  ADD COLUMN IF NOT EXISTS publication_status varchar(32) NOT NULL DEFAULT 'draft',
  ADD COLUMN IF NOT EXISTS publication_error varchar(64),
  ADD COLUMN IF NOT EXISTS publication_generation bigint NOT NULL DEFAULT 0;

UPDATE knowledge.documents
SET publication_status = CASE WHEN published THEN 'published' ELSE 'draft' END
WHERE publication_status = 'draft';

ALTER TABLE knowledge.documents
  ADD CONSTRAINT documents_publication_status_check CHECK (
    publication_status IN ('draft', 'publishing', 'published', 'publish_failed', 'unpublishing', 'unpublish_failed')
  ),
  ADD CONSTRAINT documents_publication_generation_check CHECK (publication_generation >= 0);

CREATE TABLE knowledge.publication_candidates (
  document_id uuid PRIMARY KEY REFERENCES knowledge.documents(id) ON DELETE CASCADE,
  generation bigint NOT NULL CHECK (generation > 0),
  version_id uuid,
  version_sequence bigint NOT NULL CHECK (version_sequence >= 0),
  title varchar(200) NOT NULL,
  summary varchar(1000) NOT NULL DEFAULT '',
  slug varchar(80) NOT NULL,
  language varchar(16) NOT NULL,
  tags jsonb NOT NULL,
  owner_id bigint NOT NULL,
  owner_username varchar(32) NOT NULL,
  owner_avatar text NOT NULL DEFAULT '',
  content jsonb NOT NULL,
  plain_text text NOT NULL DEFAULT '',
  media_ids jsonb NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX publication_candidates_slug_uq ON knowledge.publication_candidates(lower(slug));

CREATE TABLE knowledge.published_media_references (
  document_id uuid NOT NULL REFERENCES knowledge.document_publications(document_id) ON DELETE CASCADE,
  attachment_id uuid NOT NULL,
  generation bigint NOT NULL CHECK (generation > 0),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (document_id, attachment_id)
);
CREATE INDEX published_media_attachment_idx ON knowledge.published_media_references(attachment_id, document_id);

CREATE TABLE knowledge.publication_reference_jobs (
  id uuid PRIMARY KEY,
  document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
  owner_id bigint NOT NULL,
  generation bigint NOT NULL CHECK (generation > 0),
  action varchar(16) NOT NULL CHECK (action IN ('publish', 'clear')),
  attachment_ids jsonb NOT NULL,
  state varchar(16) NOT NULL CHECK (state IN ('pending', 'staged', 'promoted')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  lease_until timestamptz,
  parked_at timestamptz,
  last_error_key varchar(64) NOT NULL DEFAULT '',
  trace_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  UNIQUE (document_id, generation)
);
CREATE INDEX publication_reference_jobs_due_idx
  ON knowledge.publication_reference_jobs(next_attempt_at, created_at, id)
  WHERE parked_at IS NULL;
