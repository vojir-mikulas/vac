-- +goose Up
-- +goose StatementBegin
-- Maintenance mode, editable maintenance page, deploy windows, and approval
-- gates (docs/plans/maintenance-mode-and-deploy-gates.md).
--
-- maintenance_mode   — operator-set manual maintenance (sticky; survives deploys)
-- maintenance_auto   — opt-in: show the page automatically during a deploy
-- maintenance_active — effective runtime flag the router reads; kept distinct from
--                      maintenance_mode so the pipeline's clear-on-exit defer can
--                      restore correctly (clear active only when mode is false).
-- maintenance_html   — custom page HTML; NULL = use the built-in default. Bounded
--                      because it rides inside Caddy's in-memory config (the page
--                      is pushed as the inline body of a static_response route).
-- deploy_window      — JSONB array of {days:[0-6], start:"HH:MM", end:"HH:MM", tz};
--                      NULL = always allowed.
ALTER TABLE apps
    ADD COLUMN maintenance_mode   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN maintenance_auto   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN maintenance_active BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN maintenance_html   TEXT,
    ADD COLUMN deploy_window      JSONB,
    ADD CONSTRAINT maintenance_html_size CHECK
        (maintenance_html IS NULL OR octet_length(maintenance_html) <= 65536);
-- +goose StatementEnd

-- +goose StatementBegin
-- Approval gate: a deploy_trigger can require manual approval before its matched
-- pushes deploy (e.g. require approval for release/* but not main).
ALTER TABLE deploy_triggers
    ADD COLUMN require_approval BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose StatementBegin
-- Two new non-terminal deployment statuses (encoded in the existing status
-- column): `scheduled` (waiting for a deploy window to open) and
-- `pending-approval` (waiting for an operator to approve). Both must count as
-- ACTIVE in the per-app uniqueness guard (migration 00062) so duplicate pending
-- rows can't stack while one is parked — recreate the partial index to include
-- them.
DROP INDEX IF EXISTS one_active_deploy_per_app;
CREATE UNIQUE INDEX one_active_deploy_per_app
    ON deployments (app_id)
    WHERE status IN ('queued','cloning','building','deploying','health-checking','scheduled','pending-approval');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS one_active_deploy_per_app;
CREATE UNIQUE INDEX one_active_deploy_per_app
    ON deployments (app_id)
    WHERE status IN ('queued','cloning','building','deploying','health-checking');
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE deploy_triggers DROP COLUMN IF EXISTS require_approval;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE apps
    DROP CONSTRAINT IF EXISTS maintenance_html_size,
    DROP COLUMN IF EXISTS maintenance_mode,
    DROP COLUMN IF EXISTS maintenance_auto,
    DROP COLUMN IF EXISTS maintenance_active,
    DROP COLUMN IF EXISTS maintenance_html,
    DROP COLUMN IF EXISTS deploy_window;
-- +goose StatementEnd
