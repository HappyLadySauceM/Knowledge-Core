CREATE INDEX IF NOT EXISTS documents_search_fields_trgm_idx
    ON knowledge.documents USING gin (title gin_trgm_ops, summary gin_trgm_ops);
DROP INDEX IF EXISTS knowledge.documents_search_trgm_idx;

CREATE INDEX IF NOT EXISTS documents_purge_due_idx
    ON knowledge.documents (purge_after, id)
    WHERE purge_after IS NOT NULL;

CREATE INDEX IF NOT EXISTS attachments_expired_upload_idx
    ON knowledge.attachments (upload_expires, id)
    WHERE status = 'pending_upload';

DROP INDEX IF EXISTS knowledge.attachment_scan_jobs_due_idx;
CREATE INDEX attachment_scan_jobs_due_idx
    ON knowledge.attachment_scan_jobs (next_attempt_at, created_at, attachment_id);

DROP INDEX IF EXISTS knowledge.outbox_pending_idx;
CREATE INDEX outbox_pending_idx
    ON knowledge.outbox (next_attempt_at, created_at, id)
    WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS outbox_published_idx
    ON knowledge.outbox (published_at)
    WHERE published_at IS NOT NULL;
