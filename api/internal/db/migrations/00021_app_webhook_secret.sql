-- +goose Up
-- +goose StatementBegin
-- Per-app push-to-deploy webhook secret (plan 01). Sealed with VAC_MASTER_KEY
-- before storage like ssh_keys / env_vars / notification URLs — the store only
-- ever sees ciphertext. NULL until the operator generates one (lazy), which is
-- what gates the inbound webhook endpoint: no secret → webhooks disabled.
ALTER TABLE apps
    ADD COLUMN webhook_secret_enc BYTEA;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN IF EXISTS webhook_secret_enc;
-- +goose StatementEnd
