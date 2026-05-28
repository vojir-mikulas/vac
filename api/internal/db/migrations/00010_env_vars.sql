-- +goose Up
-- +goose StatementBegin
-- Per-app env vars. `value` is sealed with crypto.Box (AES-256-GCM keyed by
-- VAC_MASTER_KEY); a key rotation requires re-sealing every row.
CREATE TABLE env_vars (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id     UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX env_vars_app_id_idx ON env_vars(app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS env_vars;
-- +goose StatementEnd
