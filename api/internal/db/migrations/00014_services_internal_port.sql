-- +goose Up
-- +goose StatementBegin
-- Phase 3 routes via the private vac-edge network to the container's own port,
-- not the host-published port. internal_port is the container-side port;
-- health_path is the operator-set path for Caddy's active health check.
-- The Phase 2 `domain` placeholder is superseded by the domains table (00012,
-- which already back-filled any value), so drop it here.
ALTER TABLE services ADD COLUMN internal_port INT;
ALTER TABLE services ADD COLUMN health_path TEXT;
ALTER TABLE services DROP COLUMN domain;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services ADD COLUMN domain TEXT;
ALTER TABLE services DROP COLUMN health_path;
ALTER TABLE services DROP COLUMN internal_port;
-- +goose StatementEnd
