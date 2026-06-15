-- +goose Up
-- +goose StatementBegin
-- has_volumes flags services that declare a persistent volume (any volume mount
-- other than the Docker socket). The backups UI nudges backups only on these
-- stateful services — a stateless web/API container has nothing to back up.
-- Recomputed from the compose file on every deploy (see deploy.upsertServices);
-- default FALSE so existing rows are treated as stateless until the next deploy.
ALTER TABLE services ADD COLUMN has_volumes BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN has_volumes;
-- +goose StatementEnd
