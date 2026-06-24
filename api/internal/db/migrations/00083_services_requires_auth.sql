-- +goose Up
-- +goose StatementBegin
-- requires_auth puts a service behind the VAC login gate ("VAC guard"): Caddy
-- fronts the service's HTTP route with a forward_auth handler, so an unauthenticated
-- visitor is bounced through the control-plane login and only logged-in VAC users
-- reach the app. Intended for internal tools that should be visible only to whoever
-- can sign in to VAC. Default FALSE so existing services stay publicly reachable;
-- the flag is operator-set and survives redeploys (deploy.upsertServices never
-- writes it). Orthogonal to is_private (which removes the route entirely).
ALTER TABLE services ADD COLUMN requires_auth BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE services DROP COLUMN requires_auth;
-- +goose StatementEnd
