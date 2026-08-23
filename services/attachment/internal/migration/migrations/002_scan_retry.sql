ALTER TABLE attachment.scan_jobs
  ADD COLUMN IF NOT EXISTS lease_until timestamptz,
  ADD COLUMN IF NOT EXISTS parked_at timestamptz;

CREATE INDEX IF NOT EXISTS idx_attachment_scan_jobs_due
  ON attachment.scan_jobs(next_attempt_at, lease_until)
  WHERE parked_at IS NULL;

ALTER TABLE attachment.attachments
  ADD COLUMN IF NOT EXISTS idempotency_key varchar(128) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS request_hash varchar(64) NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_attachment_owner_idempotency
  ON attachment.attachments(owner_id, idempotency_key)
  WHERE idempotency_key <> '';
