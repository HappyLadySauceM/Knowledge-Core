-- Authoritative automatic history and scope-aware page trees.
-- The legacy commits/folders remain during the compatibility window and are
-- removed only after Gateway/Web no longer call them.

ALTER TABLE knowledge.publication_candidates
    ADD COLUMN IF NOT EXISTS content_sequence bigint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS knowledge.document_history (
    id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    kind varchar(24) NOT NULL CHECK (kind IN ('automatic', 'publish', 'restore')),
    sequence bigint NOT NULL,
    metadata_revision bigint NOT NULL,
    semantic_hash varchar(64) NOT NULL,
    content jsonb NOT NULL,
    plain_text text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    contributors jsonb NOT NULL DEFAULT '[]'::jsonb,
    block_diff jsonb NOT NULL DEFAULT '[]'::jsonb,
    is_anchor boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS document_history_semantic_uq
    ON knowledge.document_history(document_id, semantic_hash);
CREATE INDEX IF NOT EXISTS document_history_timeline_idx
    ON knowledge.document_history(document_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS document_history_retention_idx
    ON knowledge.document_history(document_id, is_anchor, created_at DESC);

CREATE TABLE IF NOT EXISTS knowledge.history_activity (
    document_id uuid PRIMARY KEY REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    first_dirty_at timestamptz NOT NULL,
    last_dirty_at timestamptz NOT NULL,
    last_checkpoint_at timestamptz,
    contributors jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS history_activity_due_idx
    ON knowledge.history_activity(last_dirty_at, first_dirty_at);

CREATE TABLE IF NOT EXISTS knowledge.spaces (
    id uuid PRIMARY KEY,
    scope_type varchar(16) NOT NULL CHECK (scope_type IN ('personal', 'organization')),
    scope_id bigint NOT NULL,
    name varchar(160) NOT NULL,
    description varchar(1000) NOT NULL DEFAULT '',
    visibility varchar(24) NOT NULL DEFAULT 'members' CHECK (visibility IN ('members', 'organization')),
    public_publication_enabled boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 1,
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS spaces_scope_idx
    ON knowledge.spaces(scope_type, scope_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS knowledge.space_members (
    space_id uuid NOT NULL REFERENCES knowledge.spaces(id) ON DELETE CASCADE,
    user_id bigint NOT NULL,
    username varchar(32) NOT NULL,
    role varchar(16) NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    revision bigint NOT NULL DEFAULT 1,
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (space_id, user_id)
);
CREATE INDEX IF NOT EXISTS space_members_user_idx
    ON knowledge.space_members(user_id, space_id);

CREATE TABLE IF NOT EXISTS knowledge.page_nodes (
    id uuid PRIMARY KEY,
    scope_type varchar(16) NOT NULL CHECK (scope_type IN ('personal', 'organization')),
    scope_id bigint NOT NULL,
    space_id uuid REFERENCES knowledge.spaces(id) ON DELETE CASCADE,
    document_id uuid NOT NULL UNIQUE REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    parent_id uuid REFERENCES knowledge.page_nodes(id) ON DELETE RESTRICT,
    position bigint NOT NULL,
    depth smallint NOT NULL CHECK (depth BETWEEN 0 AND 32),
    permission_mode varchar(16) NOT NULL DEFAULT 'inherit' CHECK (permission_mode IN ('inherit', 'restricted')),
    provisioning_state varchar(16) NOT NULL DEFAULT 'ready' CHECK (provisioning_state IN ('provisioning', 'ready', 'failed')),
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK ((scope_type = 'personal' AND space_id IS NULL) OR (space_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS page_nodes_children_idx
    ON knowledge.page_nodes(scope_type, scope_id, space_id, parent_id, position, id);
CREATE INDEX IF NOT EXISTS page_nodes_space_document_idx
    ON knowledge.page_nodes(space_id, document_id);

CREATE TABLE IF NOT EXISTS knowledge.operations (
    id uuid PRIMARY KEY,
    operation_type varchar(24) NOT NULL CHECK (operation_type IN ('restore', 'copy', 'publication')),
    document_id uuid REFERENCES knowledge.documents(id) ON DELETE CASCADE,
    target_document_id uuid REFERENCES knowledge.documents(id) ON DELETE SET NULL,
    actor_id bigint NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed', 'parked')),
    aggregate_version bigint NOT NULL,
    idempotency_key varchar(128) NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts integer NOT NULL DEFAULT 0,
    error_key varchar(64) NOT NULL DEFAULT '',
    next_attempt_at timestamptz NOT NULL,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (actor_id, operation_type, idempotency_key)
);
CREATE INDEX IF NOT EXISTS operations_work_idx
    ON knowledge.operations(state, next_attempt_at, created_at);

-- Existing normal documents become personal root pages. Folders are no longer
-- an ownership boundary; their rows are retained until the compatibility API is removed.
INSERT INTO knowledge.page_nodes(
    id, scope_type, scope_id, document_id, parent_id, position, depth,
    permission_mode, provisioning_state, revision, created_at, updated_at
)
SELECT d.id, 'personal', d.owner_id, d.id, NULL,
       (extract(epoch FROM d.created_at) * 1000000)::bigint, 0,
       'inherit', 'ready', 1, d.created_at, d.updated_at
FROM knowledge.documents d
ON CONFLICT (document_id) DO NOTHING;
