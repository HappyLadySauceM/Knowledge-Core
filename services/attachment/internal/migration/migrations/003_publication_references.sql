ALTER TABLE attachment.references
  ADD COLUMN generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
  DROP CONSTRAINT references_pkey,
  ADD CONSTRAINT references_pkey PRIMARY KEY (attachment_id, ref_type, ref_id, generation);

CREATE INDEX attachment_references_lookup_idx
  ON attachment.references(ref_type, ref_id, generation, attachment_id);

CREATE TABLE attachment.reference_heads (
  ref_type varchar(32) NOT NULL,
  ref_id varchar(128) NOT NULL,
  owner_id bigint NOT NULL,
  active_generation bigint NOT NULL DEFAULT 0 CHECK (active_generation >= 0),
  active_hash char(64) NOT NULL DEFAULT '',
  staged_generation bigint CHECK (staged_generation > 0),
  staged_hash char(64) NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (ref_type, ref_id)
);
