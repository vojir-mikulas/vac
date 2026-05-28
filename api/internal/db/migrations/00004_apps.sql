-- +goose Up
-- +goose StatementBegin
CREATE TABLE apps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    git_url         TEXT NOT NULL,
    git_branch      TEXT NOT NULL DEFAULT 'main',
    git_ssh_key_id  UUID,
    compose_file    TEXT NOT NULL DEFAULT 'compose.yaml',
    status          TEXT NOT NULL DEFAULT 'created'
                    CHECK (status IN ('created', 'stopped', 'running', 'deploying', 'failed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX apps_status_idx ON apps(status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS apps;
-- +goose StatementEnd
