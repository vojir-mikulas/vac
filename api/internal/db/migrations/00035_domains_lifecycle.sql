-- +goose Up
-- Plan 09 — domain lifecycle overhaul (F1 + F3 §1 + Phase 1 schema).
--
-- F1: automatic subdomains are now a pure function of (app slug, HTTP services,
-- base domain), derived at reconcile time — not rows. Drop the stored auto rows;
-- the proxy manager re-derives and prunes their routes on first boot.
--
-- F3 §1: the overloaded advisory `cert_status` is replaced by an in-memory
-- status projection (internal/domainstatus). It was never set to `active` and
-- only ever to `error` on a route-push failure — not a value worth keeping.
--
-- Phase 1: a custom domain can be added before it is assigned to a service, so
-- (app_id, service_name) become nullable as a both-or-neither pair.

-- +goose StatementBegin
DELETE FROM domains WHERE type = 'auto';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE domains DROP COLUMN IF EXISTS cert_status;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE domains ALTER COLUMN app_id DROP NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE domains ALTER COLUMN service_name DROP NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Either both assigned or both unassigned — never a half-bound row. The
-- composite FK (MATCH SIMPLE) is not enforced when either column is NULL, so an
-- unassigned domain is allowed; a non-null pair must still reference a service.
ALTER TABLE domains
    ADD CONSTRAINT domains_assignment_paired
    CHECK ((app_id IS NULL) = (service_name IS NULL));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE domains DROP CONSTRAINT IF EXISTS domains_assignment_paired;
-- +goose StatementEnd

-- +goose StatementBegin
-- Unassigned rows can't satisfy NOT NULL; drop them before restoring the
-- constraint so the down migration doesn't fail.
DELETE FROM domains WHERE app_id IS NULL OR service_name IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE domains ALTER COLUMN app_id SET NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE domains ALTER COLUMN service_name SET NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE domains ADD COLUMN cert_status TEXT NOT NULL DEFAULT 'pending';
-- +goose StatementEnd
