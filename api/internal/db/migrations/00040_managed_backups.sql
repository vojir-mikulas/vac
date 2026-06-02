-- +goose Up
-- +goose StatementBegin
-- Track D / D1 (plan 08). Per-service backup commands run on a schedule by the
-- backup scheduler (gated by VAC_MANAGED_SERVICES). dest_config is crypto.Box-
-- sealed JSON (bucket/endpoint/keys) — the store never sees plaintext creds,
-- exactly like apps.webhook_secret_enc and env_vars.value.
CREATE TABLE backup_configs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id        UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name  TEXT NOT NULL,
    command       TEXT NOT NULL,                 -- e.g. pg_dump -U $POSTGRES_USER $POSTGRES_DB
    frequency     TEXT NOT NULL,                 -- daily | weekly
    hour_of_day   INT  NOT NULL DEFAULT 3,
    day_of_week   INT,                           -- 0-6 (Sun=0), NULL for daily
    destination   TEXT NOT NULL,                 -- local | s3
    dest_config   BYTEA,                         -- crypto.Box-sealed JSON (bucket/endpoint/keys)
    keep_count    INT  NOT NULL DEFAULT 7,
    enabled       BOOL NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, service_name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX backup_configs_app_id_idx ON backup_configs(app_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE backup_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    config_id     UUID NOT NULL REFERENCES backup_configs(id) ON DELETE CASCADE,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'running',  -- running | success | failed
    size_bytes    BIGINT,
    artifact_key  TEXT,                              -- destination path / object key
    error         TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX backup_runs_config_id_idx ON backup_runs(config_id, started_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS backup_runs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS backup_configs;
-- +goose StatementEnd
