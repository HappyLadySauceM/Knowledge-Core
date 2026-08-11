ALTER TABLE knowledge.outbox
    ADD COLUMN IF NOT EXISTS trace_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS last_error_key varchar(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS parked_at timestamptz;

DROP INDEX IF EXISTS knowledge.outbox_pending_idx;
CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON knowledge.outbox (next_attempt_at, created_at, id)
    WHERE published_at IS NULL AND parked_at IS NULL;
CREATE INDEX IF NOT EXISTS outbox_parked_idx
    ON knowledge.outbox (parked_at)
    WHERE parked_at IS NOT NULL;
