-- +goose Up
-- +goose StatementBegin
-- is_private lets an operator force a service to stay internal-only: VAC assigns
-- it no auto-domain and routes no custom domain to it, regardless of the ports it
-- exposes. Needed because some images declare a built-in EXPOSE (e.g. meilisearch
-- EXPOSE 7700) that `docker compose ps` surfaces as a routable port even when the
-- compose file never asked to publish it. Default FALSE so existing services keep
-- their current (auto-routed) behavior; the flag is operator-set and survives
-- redeploys (deploy.upsertServices never writes it).
ALTER TABLE services ADD COLUMN is_private BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN is_private;
-- +goose StatementEnd
