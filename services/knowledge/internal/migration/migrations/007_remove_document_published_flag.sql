-- Publication status is the single source of truth.  Keep the wire-level
-- `published` field computed for compatibility, but stop persisting a second
-- mutable flag that can diverge from the publication snapshot.
DROP INDEX IF EXISTS knowledge.documents_public_idx;
ALTER TABLE knowledge.documents
  DROP CONSTRAINT IF EXISTS documents_publication_check,
  DROP CONSTRAINT IF EXISTS documents_deletion_check,
  DROP COLUMN IF EXISTS published;

-- Keep the deletion invariant after removing the duplicated boolean. A
-- document marked for trash/purge can never remain publicly published.
ALTER TABLE knowledge.documents
  ADD CONSTRAINT documents_deletion_state_check CHECK (
    (deleted_at IS NULL AND purge_after IS NULL)
    OR (deleted_at IS NOT NULL AND purge_after IS NOT NULL AND publication_status <> 'published')
  );

CREATE INDEX IF NOT EXISTS documents_publication_idx
  ON knowledge.documents (publication_status, published_at DESC, id DESC)
  WHERE deleted_at IS NULL;
