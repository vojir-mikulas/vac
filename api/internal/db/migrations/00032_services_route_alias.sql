-- +goose Up
-- +goose StatementBegin
-- Zero-downtime / rolling deploys (A3). During a roll the new and old
-- generations of a stateless HTTP service run simultaneously under DISTINCT
-- vac-edge aliases (one alias can't deterministically point at two containers).
-- route_alias is the live alias Caddy's route should dial — set to the new
-- generation alias `{slug}--{service}--{gen}` on a successful cutover.
--
-- NULL/empty means "no override": dial/routeFor fall back to the bare
-- `{slug}--{service}` alias, so existing apps and the non-rolling path are
-- byte-for-byte unchanged.
ALTER TABLE services ADD COLUMN route_alias TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN IF EXISTS route_alias;
-- +goose StatementEnd
