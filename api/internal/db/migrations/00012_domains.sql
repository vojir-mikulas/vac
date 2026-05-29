-- +goose Up
-- +goose StatementBegin
-- Hostnames routed to a service via Caddy. One hostname → exactly one service
-- (UNIQUE). `type` distinguishes VAC-assigned automatic subdomains from
-- operator-added custom domains; both route simultaneously. The composite FK
-- ties a domain to a concrete service row so it cascades when the service is
-- removed from the compose project. `cert_status` is advisory only — routing
-- does not depend on it.
CREATE TABLE domains (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id       UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    hostname     TEXT NOT NULL,
    type         TEXT NOT NULL CHECK (type IN ('auto', 'custom')),
    cert_status  TEXT NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (hostname),
    FOREIGN KEY (app_id, service_name) REFERENCES services(app_id, service_name) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX domains_app_id_idx ON domains(app_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Preserve any custom domain set via the Phase 2 services.domain placeholder
-- before 00014 drops that column.
INSERT INTO domains (app_id, service_name, hostname, type)
SELECT app_id, service_name, domain, 'custom'
FROM services
WHERE domain IS NOT NULL AND domain <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS domains;
-- +goose StatementEnd
