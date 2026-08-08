DROP INDEX IF EXISTS collaboration.projection_jobs_due_idx;
CREATE INDEX projection_jobs_due_idx
    ON collaboration.projection_jobs (next_attempt_at, updated_at, document_id);

DROP INDEX IF EXISTS collaboration.outbox_due_idx;
CREATE INDEX outbox_due_idx
    ON collaboration.outbox (next_attempt_at, created_at, id)
    WHERE published_at IS NULL;
CREATE INDEX outbox_published_idx
    ON collaboration.outbox (published_at)
    WHERE published_at IS NOT NULL;

CREATE INDEX idempotency_resource_idx
    ON collaboration.idempotency_keys (resource_id);
