ALTER TABLE knowledge.documents
  ADD COLUMN IF NOT EXISTS language varchar(16) NOT NULL DEFAULT 'zh-CN';

CREATE TABLE IF NOT EXISTS knowledge.folders (
  id uuid PRIMARY KEY,
  owner_id bigint NOT NULL,
  parent_id uuid REFERENCES knowledge.folders(id) ON DELETE CASCADE,
  name varchar(120) NOT NULL,
  depth integer NOT NULL DEFAULT 1 CHECK (depth BETWEEN 1 AND 8),
  revision bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner_id, id),
  UNIQUE (owner_id, parent_id, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS folders_owner_root_name_uq
  ON knowledge.folders(owner_id, lower(name)) WHERE parent_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS folders_owner_parent_name_uq
  ON knowledge.folders(owner_id, parent_id, lower(name)) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS folders_owner_parent_idx ON knowledge.folders(owner_id, parent_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS knowledge.document_placements (
  owner_id bigint NOT NULL,
  document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
  folder_id uuid REFERENCES knowledge.folders(id) ON DELETE SET NULL,
  revision bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (owner_id, document_id)
);
CREATE INDEX IF NOT EXISTS document_placements_folder_idx
  ON knowledge.document_placements(owner_id, folder_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS knowledge.tags (
  id uuid PRIMARY KEY,
  owner_id bigint NOT NULL,
  name varchar(64) NOT NULL,
  slug varchar(80) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner_id, slug)
);
CREATE TABLE IF NOT EXISTS knowledge.document_tags (
  document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
  tag_id uuid NOT NULL REFERENCES knowledge.tags(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (document_id, tag_id)
);
CREATE INDEX IF NOT EXISTS document_tags_tag_idx ON knowledge.document_tags(tag_id, document_id);

CREATE TABLE IF NOT EXISTS knowledge.document_publications (
  document_id uuid PRIMARY KEY REFERENCES knowledge.documents(id) ON DELETE CASCADE,
  version_id uuid,
  version_sequence bigint NOT NULL DEFAULT 0,
  title varchar(200) NOT NULL,
  summary varchar(1000) NOT NULL DEFAULT '',
  slug varchar(80) NOT NULL,
  language varchar(16) NOT NULL DEFAULT 'zh-CN',
  tags jsonb NOT NULL DEFAULT '[]'::jsonb,
  owner_id bigint NOT NULL,
  owner_username varchar(32) NOT NULL,
  owner_avatar text NOT NULL DEFAULT '',
  content jsonb NOT NULL,
  plain_text text NOT NULL DEFAULT '',
  published_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS document_publications_slug_uq
  ON knowledge.document_publications(lower(slug));

CREATE TABLE IF NOT EXISTS knowledge.publication_attachments (
  document_id uuid NOT NULL REFERENCES knowledge.document_publications(document_id) ON DELETE CASCADE,
  attachment_id uuid NOT NULL REFERENCES knowledge.attachments(id) ON DELETE RESTRICT,
  PRIMARY KEY (document_id, attachment_id)
);

INSERT INTO knowledge.document_publications(
  document_id, version_sequence, title, summary, slug, language, tags,
  owner_id, owner_username, owner_avatar, content, plain_text, published_at, updated_at
)
SELECT d.id, p.sequence, d.title, d.summary, d.slug, d.language, '[]'::jsonb,
       d.owner_id, d.owner_username, d.owner_avatar, p.content, p.plain_text,
       COALESCE(d.published_at, d.updated_at), COALESCE(d.published_at, d.updated_at)
FROM knowledge.documents d
JOIN knowledge.document_projections p ON p.document_id = d.id
WHERE d.published = true
ON CONFLICT (document_id) DO NOTHING;
