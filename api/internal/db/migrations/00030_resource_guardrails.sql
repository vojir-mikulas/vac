-- +goose Up
-- +goose StatementBegin
-- Per-app RAM limit (plan 06). NULL = unlimited / use the box default; a value
-- is the hard memory ceiling in mebibytes, wired into the deploy as a compose
-- `mem_limit`. A first-class column (not stashed in build_config JSON) because
-- the box-budget panel SUMs it across all apps — a relational aggregate, not an
-- opaque blob.
--
-- Track B owns migration range 00030–00039 (see docs/plans/upcoming/
-- track-b-execution.md); this leaves 00021–00029 for the Deploy Core track so
-- the two don't renumber each other at merge.
ALTER TABLE apps
    ADD COLUMN mem_limit_mb INT;
-- +goose StatementEnd

-- +goose StatementBegin
-- Count of times a service's container was OOM-killed, surfaced distinctly from
-- ordinary crashes in the UI and notified on. Parallel to restart_count.
ALTER TABLE services
    ADD COLUMN oom_killed_count INT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN IF EXISTS oom_killed_count;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN IF EXISTS mem_limit_mb;
-- +goose StatementEnd
