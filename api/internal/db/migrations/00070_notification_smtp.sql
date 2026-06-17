-- +goose Up
-- +goose StatementBegin
-- Email (SMTP) notification channel — a fourth channel alongside Discord/Slack.
-- Only the SMTP password is a bearer secret, so it is sealed with VAC_MASTER_KEY
-- (BYTEA ciphertext, like discord_url_enc); host/port/username/from/to/tls_mode
-- are operator-set config, stored plaintext. An empty smtp_host means the email
-- channel is off. tls_mode is one of starttls (default) / implicit / none.
ALTER TABLE notification_settings
    ADD COLUMN smtp_host         TEXT,
    ADD COLUMN smtp_port         INT,
    ADD COLUMN smtp_username     TEXT,
    ADD COLUMN smtp_password_enc BYTEA,
    ADD COLUMN smtp_from         TEXT,
    ADD COLUMN smtp_to           TEXT,
    ADD COLUMN smtp_tls_mode     TEXT NOT NULL DEFAULT 'starttls';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE notification_settings
    DROP COLUMN smtp_host,
    DROP COLUMN smtp_port,
    DROP COLUMN smtp_username,
    DROP COLUMN smtp_password_enc,
    DROP COLUMN smtp_from,
    DROP COLUMN smtp_to,
    DROP COLUMN smtp_tls_mode;
-- +goose StatementEnd
