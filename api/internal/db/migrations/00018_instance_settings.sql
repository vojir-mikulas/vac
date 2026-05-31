-- +goose Up
-- +goose StatementBegin
-- Singleton instance-wide settings (single-tenant control plane). Holds the
-- runtime-editable base domain used for automatic subdomains — previously
-- config-only (VAC_BASE_DOMAIN). An empty string means "unset / use config".
-- See docs/deviations.md for why this lives in the DB rather than config.
CREATE TABLE instance_settings (
    id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    base_domain TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Seed the single row so get/put is always a plain UPDATE of id = 1.
INSERT INTO instance_settings (id) VALUES (1) ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS instance_settings;
-- +goose StatementEnd
