DROP INDEX IF EXISTS knowledge.documents_published_revision_idx;

ALTER TABLE knowledge.documents
    DROP CONSTRAINT IF EXISTS documents_published_revision_owner_fkey,
    DROP COLUMN IF EXISTS published_revision_id;

ALTER TABLE knowledge.document_revisions
    DROP CONSTRAINT IF EXISTS document_revisions_document_id_id_key;

DROP INDEX IF EXISTS knowledge.document_revisions_search_vector_idx;

ALTER TABLE knowledge.document_revisions
    DROP COLUMN IF EXISTS search_vector;

ALTER TABLE knowledge.document_ops
    DROP CONSTRAINT IF EXISTS document_ops_base_block_version_check,
    DROP COLUMN IF EXISTS base_block_version;
