-- +goose Up
-- +goose StatementBegin
-- Per-app edge rate limiting. rate_limit_rpm caps requests per minute per client
-- IP at Caddy (via the caddy-ratelimit handler the proxy image bakes in). NULL or
-- 0 means no limit. Applied to every HTTP route of the app on the next Sync.
ALTER TABLE apps
    ADD COLUMN rate_limit_rpm INT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps
    DROP COLUMN IF EXISTS rate_limit_rpm;
-- +goose StatementEnd
