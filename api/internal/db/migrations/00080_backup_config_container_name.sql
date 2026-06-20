-- +goose Up
-- +goose StatementBegin
-- Track D / D1 fix. backup_configs.service_name was overloaded as three things at
-- once: the unique identity (app_id, service_name), the container to `docker exec`
-- the dump in, and the artifact-path component. For a managed DB that lives in a
-- shared engine container (vac-db, vac-mariadb), service_name was the *container*
-- name — so two managed DBs of the same engine on one app collided on the UNIQUE
-- constraint and only the first was ever backed up (the second's data was silently
-- uncovered; see docs/deviations.md "two-managed-DBs" note).
--
-- Split the exec target out into its own nullable column so service_name can go
-- back to being a pure per-DB identity. When container_name is set the backup
-- engine execs into it directly; when NULL it resolves the app's service row as
-- before. This is additive and backward-compatible: existing rows keep
-- container_name = NULL and continue to resolve via the service-row / literal
-- fallback, so no data rewrite is needed.
ALTER TABLE backup_configs ADD COLUMN container_name TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE backup_configs DROP COLUMN container_name;
-- +goose StatementEnd
