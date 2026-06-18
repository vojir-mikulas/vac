-- +goose Up
-- +goose StatementBegin
-- Scale-to-zero: idle suspend + wake-on-request (docs/plans/scale-to-zero.md).
--
-- idle_suspend_enabled — per-app opt-in (master gate is VAC_IDLE_SUSPEND).
-- idle_timeout_minutes — inactivity window before suspend; NULL = use the
--                        instance default (VAC_IDLE_TIMEOUT).
-- suspended            — current runtime state; when true the app's containers
--                        are stopped and its hosts serve a wake route. Must be in
--                        the SELECT list so proxy.Reconcile installs wake routes
--                        (not normal routes) on boot.
-- last_traffic_at      — denormalized last-seen request time, stamped by the
--                        sweeper from request_metrics (advisory only).
ALTER TABLE apps
    ADD COLUMN idle_suspend_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN idle_timeout_minutes INT,
    ADD COLUMN suspended            BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN last_traffic_at      TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps
    DROP COLUMN IF EXISTS idle_suspend_enabled,
    DROP COLUMN IF EXISTS idle_timeout_minutes,
    DROP COLUMN IF EXISTS suspended,
    DROP COLUMN IF EXISTS last_traffic_at;
-- +goose StatementEnd
