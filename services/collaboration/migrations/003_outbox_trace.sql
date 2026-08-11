ALTER TABLE collaboration.outbox
    ADD COLUMN IF NOT EXISTS trace_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS parked_at timestamptz;

DROP INDEX IF EXISTS collaboration.outbox_due_idx;
CREATE INDEX IF NOT EXISTS outbox_due_idx
    ON collaboration.outbox (next_attempt_at, created_at, id)
    WHERE published_at IS NULL AND parked_at IS NULL;
CREATE INDEX IF NOT EXISTS outbox_parked_idx
    ON collaboration.outbox (parked_at)
    WHERE parked_at IS NOT NULL;
