-- +goose Up
-- +goose StatementBegin
-- Lock the deployment-history schema shape before the Deploy Core track (plans
-- 02 rollback, 01 push-to-deploy) starts, so its migrations don't churn.
--
-- `triggered_by` records *why* a deployment happened. Values live in Go
-- (manual|push|tag|rollback|system). Existing rows default to 'manual' — that
-- is what every deploy was until push-to-deploy lands.
ALTER TABLE deployments
    ADD COLUMN triggered_by TEXT NOT NULL DEFAULT 'manual';
-- +goose StatementEnd

-- +goose StatementBegin
-- A rollback (plan 02) is recorded as a *new* deployment that points at the one
-- it restored — history is append-only, never mutated. ON DELETE SET NULL so
-- pruning the source deployment doesn't cascade-delete the rollback record.
ALTER TABLE deployments
    ADD COLUMN rolled_back_from UUID REFERENCES deployments(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-app push-to-deploy rules (plan 01). Decision: a dedicated table over a
-- JSONB column on apps — rules are a queryable list ("which apps deploy on
-- tag:v*"), match the relational style of the rest of the schema, and a webhook
-- match is a plain WHERE rather than JSON traversal.
--
-- `event` ∈ push|tag|manual; `filter` is a branch/tag glob ('' = match any).
-- Enum values live in Go, consistent with the other status/kind columns.
CREATE TABLE deploy_triggers (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id     UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    event      TEXT NOT NULL,
    filter     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX deploy_triggers_app_idx ON deploy_triggers(app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS deploy_triggers;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE deployments DROP COLUMN IF EXISTS rolled_back_from;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE deployments DROP COLUMN IF EXISTS triggered_by;
-- +goose StatementEnd
