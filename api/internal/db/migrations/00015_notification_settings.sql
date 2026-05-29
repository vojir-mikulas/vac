-- +goose Up
-- +goose StatementBegin
-- Singleton settings row (single-tenant control plane). Webhook URLs are
-- sealed with VAC_MASTER_KEY before storage, like env_vars / ssh_keys — the
-- store only ever sees ciphertext. `events` is a per-event enable map; an
-- absent key defaults to on for the implemented events.
CREATE TABLE notification_settings (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    discord_url_enc BYTEA,
    slack_url_enc   BYTEA,
    events          JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Seed the single row so get/put is always a plain UPDATE of id = 1.
INSERT INTO notification_settings (id) VALUES (1) ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_settings;
-- +goose StatementEnd
