CREATE TABLE IF NOT EXISTS attachment.attachments (
  id uuid PRIMARY KEY,
  owner_id bigint NOT NULL,
  filename varchar(255) NOT NULL,
  media_type varchar(127) NOT NULL,
  category varchar(32) NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 1073741824),
  sha256 varchar(64) NOT NULL,
  detected_type varchar(127) NOT NULL DEFAULT '',
  object_key varchar(512) NOT NULL UNIQUE,
  upload_id varchar(256) NOT NULL,
  status varchar(32) NOT NULL,
  part_size bigint NOT NULL,
  part_count integer NOT NULL CHECK (part_count > 0 AND part_count <= 64),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_attachment_owner_status ON attachment.attachments(owner_id,status,created_at DESC);
CREATE TABLE IF NOT EXISTS attachment.scan_jobs (
  attachment_id uuid PRIMARY KEY REFERENCES attachment.attachments(id) ON DELETE CASCADE,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL,
  last_error varchar(255) NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS attachment.references (
  attachment_id uuid NOT NULL REFERENCES attachment.attachments(id) ON DELETE RESTRICT,
  ref_type varchar(32) NOT NULL,
  ref_id varchar(128) NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (attachment_id,ref_type,ref_id)
);
