-- Keep one rolling, user-visible recovery point per document.  Publication
-- snapshots are not stored in this table and are therefore unaffected.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY document_id
               ORDER BY created_at DESC, id DESC
           ) AS position
    FROM collaboration.versions
), removed AS (
    DELETE FROM collaboration.versions AS version
    USING ranked
    WHERE version.id = ranked.id
      AND ranked.position > 1
    RETURNING version.id
)
DELETE FROM collaboration.idempotency_keys AS key
WHERE key.resource_id IN (SELECT id FROM removed);

UPDATE collaboration.versions
SET kind = 'automatic', label = NULL;

CREATE UNIQUE INDEX versions_document_id_unique_idx
    ON collaboration.versions (document_id);

-- Keep the exact state returned by a write idempotent even while the rolling
-- recovery row is advanced by a later automatic checkpoint. These columns are
-- bound to the idempotency key, not the singleton version row, so concurrent
-- publication retries cannot overwrite one another's payload.
ALTER TABLE collaboration.idempotency_keys
    ADD COLUMN version_sequence bigint,
    ADD COLUMN version_state bytea;

ALTER TABLE collaboration.idempotency_keys
    ADD CONSTRAINT idempotency_version_snapshot_check CHECK (
        (version_sequence IS NULL AND version_state IS NULL)
        OR (version_sequence >= 0 AND octet_length(version_state) > 0)
    );
