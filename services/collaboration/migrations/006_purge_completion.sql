-- Permanent deletion is retried across Collaboration and Knowledge.  The
-- durable marker makes the Collaboration side idempotent and prevents a
-- retry from advancing the document generation a second time.
ALTER TABLE collaboration.documents
    ADD COLUMN purged_at timestamptz;
