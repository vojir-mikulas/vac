-- +goose Up
-- +goose StatementBegin
-- Build adapters (plan 03): how VAC turns a repo into a runnable stack.
--   build_kind   — auto | compose | dockerfile | framework | static
--   build_config — adapter-specific knobs, e.g. {composePath}, {dockerfilePath},
--                  {framework,buildCommand,startCommand,port}, {staticDir,spaFallback}
-- compose_file is kept for back-compat: the compose adapter still reads it when
-- build_config.composePath is empty (see docs/deviations.md).
ALTER TABLE apps ADD COLUMN build_kind TEXT NOT NULL DEFAULT 'auto';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE apps ADD COLUMN build_config JSONB NOT NULL DEFAULT '{}'::jsonb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN build_config;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN build_kind;
-- +goose StatementEnd
