-- +goose Up
-- +goose StatementBegin
-- Custom-domain DNS automation (docs/plans/dns-automation-and-byo-cert.md Part
-- A). Credentials are instance-wide (one operator, one box — matches base_domain
-- and the singleton instance_settings row), not per-domain. dns_provider is ''
-- (off) or 'cloudflare'; the API token is a bearer secret, sealed with
-- VAC_MASTER_KEY (crypto.Box ciphertext) like smtp_password_enc; dns_zone is the
-- zone name the records live under (e.g. "example.com").
ALTER TABLE instance_settings
    ADD COLUMN dns_provider           TEXT NOT NULL DEFAULT '',
    ADD COLUMN dns_provider_token_enc BYTEA,
    ADD COLUMN dns_zone               TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE instance_settings
    DROP COLUMN IF EXISTS dns_provider,
    DROP COLUMN IF EXISTS dns_provider_token_enc,
    DROP COLUMN IF EXISTS dns_zone;
-- +goose StatementEnd
