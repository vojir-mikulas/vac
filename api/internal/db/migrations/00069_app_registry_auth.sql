-- +goose Up
-- +goose StatementBegin
-- Per-app private-registry credentials for image-sourced apps (deploy from a
-- prebuilt image). Holds a crypto.Box-sealed JSON {registry, username, password}
-- — sealed with VAC_MASTER_KEY before storage like ssh_keys / env_vars /
-- webhook_secret_enc, so the store only ever sees ciphertext. NULL = public
-- image, no `docker login` before pull.
ALTER TABLE apps
    ADD COLUMN registry_auth_enc BYTEA;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN IF EXISTS registry_auth_enc;
-- +goose StatementEnd
