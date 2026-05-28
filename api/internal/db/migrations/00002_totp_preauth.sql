-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions
    ADD COLUMN pre_auth BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX sessions_pre_auth_idx ON sessions(pre_auth) WHERE pre_auth = TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS sessions_pre_auth_idx;
ALTER TABLE sessions DROP COLUMN IF EXISTS pre_auth;
-- +goose StatementEnd
