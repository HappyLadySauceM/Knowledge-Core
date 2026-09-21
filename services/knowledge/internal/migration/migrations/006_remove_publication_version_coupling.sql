-- Publication snapshots are now the latest published state, not a history
-- record. Existing snapshots remain intact while their version-only columns
-- are removed in this forward migration.
ALTER TABLE knowledge.document_publications
    DROP COLUMN IF EXISTS version_id,
    DROP COLUMN IF EXISTS version_sequence;

ALTER TABLE knowledge.publication_candidates
    DROP COLUMN IF EXISTS version_id,
    DROP COLUMN IF EXISTS version_sequence;
