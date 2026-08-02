export const initialMigration = `
CREATE SCHEMA IF NOT EXISTS collaboration;

CREATE TABLE IF NOT EXISTS collaboration.schema_migrations (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS collaboration.document_heads (
  document_id uuid PRIMARY KEY,
  current_sequence bigint NOT NULL DEFAULT 0 CHECK (current_sequence >= 0),
  last_snapshot_sequence bigint NOT NULL DEFAULT 0 CHECK (last_snapshot_sequence >= 0),
  last_version_sequence bigint NOT NULL DEFAULT 0 CHECK (last_version_sequence >= 0),
  last_automatic_version_at timestamptz,
  last_actor_id bigint,
  last_actor_username varchar(32),
  last_actor_avatar text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CONSTRAINT document_heads_actor_check CHECK (
    (last_actor_id IS NULL AND last_actor_username IS NULL AND last_actor_avatar IS NULL) OR
    (last_actor_id > 0 AND last_actor_username IS NOT NULL AND last_actor_avatar IS NOT NULL)
  ),
  CONSTRAINT document_heads_sequence_check CHECK (
    last_snapshot_sequence <= current_sequence AND last_version_sequence <= current_sequence
  )
);

CREATE TABLE IF NOT EXISTS collaboration.document_updates (
  document_id uuid NOT NULL REFERENCES collaboration.document_heads(document_id) ON DELETE CASCADE,
  sequence bigint NOT NULL CHECK (sequence > 0),
  update bytea NOT NULL CHECK (octet_length(update) > 0),
  actor_id bigint NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (document_id, sequence)
);
CREATE INDEX IF NOT EXISTS document_updates_created_idx
  ON collaboration.document_updates (created_at, document_id, sequence);

CREATE TABLE IF NOT EXISTS collaboration.document_snapshots (
  document_id uuid NOT NULL REFERENCES collaboration.document_heads(document_id) ON DELETE CASCADE,
  sequence bigint NOT NULL CHECK (sequence >= 0),
  state bytea NOT NULL CHECK (octet_length(state) > 0),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (document_id, sequence)
);

CREATE TABLE IF NOT EXISTS collaboration.document_versions (
  id uuid PRIMARY KEY,
  document_id uuid NOT NULL REFERENCES collaboration.document_heads(document_id) ON DELETE CASCADE,
  sequence bigint NOT NULL CHECK (sequence >= 0),
  kind varchar(16) NOT NULL CHECK (kind IN ('manual', 'automatic', 'restoration')),
  label varchar(200),
  state bytea NOT NULL CHECK (octet_length(state) > 0),
  created_by_id bigint NOT NULL,
  created_by_username varchar(32) NOT NULL,
  created_by_avatar text NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS document_versions_page_idx
  ON collaboration.document_versions (document_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS collaboration.projection_jobs (
  document_id uuid PRIMARY KEY REFERENCES collaboration.document_heads(document_id) ON DELETE CASCADE,
  target_sequence bigint NOT NULL CHECK (target_sequence >= 0),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at timestamptz NOT NULL,
  last_error_key varchar(64) NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS projection_jobs_due_idx
  ON collaboration.projection_jobs (next_attempt_at, updated_at);

CREATE TABLE IF NOT EXISTS collaboration.idempotency_keys (
  actor_id bigint NOT NULL,
  operation varchar(128) NOT NULL,
  key varchar(128) NOT NULL,
  request_hash char(64) NOT NULL,
  resource_id uuid NOT NULL REFERENCES collaboration.document_versions(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (actor_id, operation, key)
);
CREATE INDEX IF NOT EXISTS collaboration_idempotency_expiry_idx
  ON collaboration.idempotency_keys (expires_at);
`;
