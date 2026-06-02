-- +goose Up
-- +goose StatementBegin
-- Track D / D2 (plan 09). One row per app-owned managed database. The shared
-- per-engine instances (vac-mariadb, vac-redis, …) are NOT rows here — they're
-- managed containers tracked by fixed name; "is the engine up?" is a docker
-- query, not DB state. secret_enc is the crypto.Box-sealed connection string +
-- password; the store never sees plaintext.
CREATE TABLE managed_databases (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    engine          TEXT NOT NULL,                 -- postgres | mariadb | mongo | redis
    db_name         TEXT NOT NULL,                 -- provisioned database / keyspace
    role_name       TEXT,                          -- provisioned role (NULL for redis)
    secret_enc      BYTEA NOT NULL,                -- crypto.Box-sealed connection string + password
    env_var_name    TEXT NOT NULL DEFAULT 'DATABASE_URL',
    status          TEXT NOT NULL DEFAULT 'provisioning', -- provisioning | ready | error
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (app_id, engine, db_name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX managed_databases_app_id_idx ON managed_databases(app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS managed_databases;
-- +goose StatementEnd
