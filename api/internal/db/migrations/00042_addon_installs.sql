-- +goose Up
-- +goose StatementBegin
-- Track D / D3 (plan 12). A catalog install IS an app — the catalog itself is
-- embedded data, not a table. We add only provenance so the pipeline can branch
-- the clone step (template source → materialize embedded files instead of git
-- clone) and the UI can label installs with their template.
ALTER TABLE apps ADD COLUMN source      TEXT NOT NULL DEFAULT 'git'; -- git | template
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE apps ADD COLUMN template_id TEXT;                        -- e.g. 'grafana'
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN IF EXISTS template_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE apps DROP COLUMN IF EXISTS source;
-- +goose StatementEnd
