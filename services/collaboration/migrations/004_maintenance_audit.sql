ALTER TABLE collaboration.outbox
    ADD COLUMN IF NOT EXISTS redrive_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_redrive_at timestamptz;

ALTER TABLE collaboration.outbox
    DROP CONSTRAINT IF EXISTS outbox_redrive_count_check;

ALTER TABLE collaboration.outbox
    ADD CONSTRAINT outbox_redrive_count_check CHECK (redrive_count >= 0);

CREATE TABLE IF NOT EXISTS collaboration.maintenance_actions (
    id uuid PRIMARY KEY,
    action varchar(64) NOT NULL,
    target_kind varchar(64) NOT NULL,
    target_id uuid,
    operator varchar(128) NOT NULL,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS maintenance_actions_target_idx
    ON collaboration.maintenance_actions (target_kind, target_id, created_at DESC);
