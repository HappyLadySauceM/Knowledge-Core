-- The collaboration service now stores only the live CRDT document state.
-- The latest public snapshot is owned by Knowledge; no historical versions are
-- part of the runtime contract.  Keep earlier migrations immutable and remove
-- their tables/columns with this forward migration.
DROP INDEX IF EXISTS collaboration.versions_page_idx;
DROP INDEX IF EXISTS collaboration.versions_document_id_unique_idx;
DROP TABLE IF EXISTS collaboration.versions;

ALTER TABLE collaboration.documents
    DROP CONSTRAINT IF EXISTS documents_watermark_check,
    DROP CONSTRAINT IF EXISTS documents_actor_check,
    DROP COLUMN IF EXISTS last_version_sequence,
    DROP COLUMN IF EXISTS last_automatic_version_at,
    DROP COLUMN IF EXISTS last_actor_id,
    DROP COLUMN IF EXISTS last_actor_username,
    DROP COLUMN IF EXISTS last_actor_avatar;

ALTER TABLE collaboration.idempotency_keys
    DROP CONSTRAINT IF EXISTS idempotency_version_snapshot_check,
    DROP COLUMN IF EXISTS version_sequence,
    DROP COLUMN IF EXISTS version_state;
