-- +goose Up
-- +goose StatementBegin
-- One row per deploy attempt. `status` enum lives in Go (see services.status).
-- Allowed values: queued, cloning, building, deploying, health-checking,
-- running, error, interrupted.
CREATE TABLE deployments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id         UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status         TEXT NOT NULL DEFAULT 'queued',
    triggered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    compose_hash   TEXT,
    commit_sha     TEXT,
    commit_message TEXT,
    error          TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
-- History queries always filter by app and sort by triggered_at DESC.
CREATE INDEX deployments_app_triggered_idx
    ON deployments(app_id, triggered_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS deployments;
-- +goose StatementEnd
