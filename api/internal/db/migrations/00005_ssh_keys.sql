-- +goose Up
-- +goose StatementBegin
CREATE TABLE ssh_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id      UUID NOT NULL UNIQUE REFERENCES apps(id) ON DELETE CASCADE,
    public_key  TEXT NOT NULL,
    private_key BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One key per app — apps.git_ssh_key_id from 00004 is redundant once the
-- ssh_keys.app_id UNIQUE relationship exists.
ALTER TABLE apps DROP COLUMN IF EXISTS git_ssh_key_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps ADD COLUMN git_ssh_key_id UUID;
DROP TABLE IF EXISTS ssh_keys;
-- +goose StatementEnd
