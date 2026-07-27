CREATE SCHEMA IF NOT EXISTS identity;

CREATE TABLE identity.users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username VARCHAR(32) NOT NULL,
    email VARCHAR(320) NOT NULL,
    password_hash TEXT NOT NULL,
    role VARCHAR(16) NOT NULL DEFAULT 'user',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    token_version BIGINT NOT NULL DEFAULT 1,
    avatar TEXT NOT NULL DEFAULT '',
    bio VARCHAR(500) NOT NULL DEFAULT '',
    failed_login_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_username_length_check CHECK (char_length(username) BETWEEN 3 AND 32),
    CONSTRAINT users_role_check CHECK (role IN ('admin', 'user')),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT users_token_version_check CHECK (token_version >= 1),
    CONSTRAINT users_failed_login_attempts_check CHECK (failed_login_attempts >= 0)
);

CREATE UNIQUE INDEX users_username_lower_uidx ON identity.users (lower(username));
CREATE UNIQUE INDEX users_email_lower_uidx ON identity.users (lower(email));
CREATE INDEX users_status_created_at_idx ON identity.users (status, created_at DESC);
