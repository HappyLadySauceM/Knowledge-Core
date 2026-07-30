ALTER TABLE knowledge.document_ops
    ADD COLUMN base_block_version BIGINT;

UPDATE knowledge.document_ops
SET base_block_version = block_version - 1;

ALTER TABLE knowledge.document_ops
    ALTER COLUMN base_block_version SET NOT NULL,
    ADD CONSTRAINT document_ops_base_block_version_check CHECK (base_block_version >= 0);

ALTER TABLE knowledge.document_revisions
    ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(summary, ''))
    ) STORED;

CREATE INDEX document_revisions_search_vector_idx
    ON knowledge.document_revisions USING GIN (search_vector);

ALTER TABLE knowledge.document_revisions
    ADD CONSTRAINT document_revisions_document_id_id_key UNIQUE (document_id, id);

ALTER TABLE knowledge.documents
    ADD COLUMN published_revision_id BIGINT,
    ADD CONSTRAINT documents_published_revision_owner_fkey
        FOREIGN KEY (id, published_revision_id)
        REFERENCES knowledge.document_revisions(document_id, id)
        ON DELETE NO ACTION
        DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX documents_published_revision_idx
    ON knowledge.documents (published_revision_id)
    WHERE status = 'published';

UPDATE knowledge.documents AS document
SET published_revision_id = (
    SELECT id
    FROM knowledge.document_revisions
    WHERE document_id = document.id
    ORDER BY created_at DESC, id DESC
    LIMIT 1
);
