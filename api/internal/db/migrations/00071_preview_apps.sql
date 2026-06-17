-- +goose Up
-- +goose StatementBegin
-- Preview deployments (docs/plans/preview-deployments.md): a preview is just an
-- app with is_preview=true and parent_app_id set. It reuses the entire deploy
-- pipeline, router, and teardown path; only the lifecycle (create-on-branch,
-- reap-on-close/TTL) is new. Deleting the parent reaps all its previews via the
-- ON DELETE CASCADE self-reference. last_preview_push_at stamps the most recent
-- push so the TTL expirer can reap previews idle past VAC_PREVIEW_TTL.
ALTER TABLE apps
    ADD COLUMN is_preview           BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN parent_app_id        UUID REFERENCES apps(id) ON DELETE CASCADE,
    ADD COLUMN last_preview_push_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
-- Partial index: the only queries that touch parent_app_id are preview lookups
-- (list-for-parent, find-by-parent+branch), so index just the preview rows.
CREATE INDEX apps_parent_app_id_idx ON apps(parent_app_id) WHERE parent_app_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS apps_parent_app_id_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE apps
    DROP COLUMN is_preview,
    DROP COLUMN parent_app_id,
    DROP COLUMN last_preview_push_at;
-- +goose StatementEnd
