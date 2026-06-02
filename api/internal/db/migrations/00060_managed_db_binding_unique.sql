-- +goose Up
-- +goose StatementBegin
-- Track P1 (triage). A managed DB's connection string is injected as an env var
-- named by env_var_name. Two managed DBs on one app that share a binding name
-- (the historic DATABASE_URL collision — both engines defaulted to it) would
-- silently overwrite each other's env var.
--
-- Heal any pre-existing collisions first: keep the oldest row's binding and
-- suffix later duplicates (_2, _3, …) so the unique index below can be created
-- on installs that already hit the bug.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY app_id, env_var_name ORDER BY created_at, id) AS rn
    FROM managed_databases
)
UPDATE managed_databases m
SET env_var_name = m.env_var_name || '_' || r.rn
FROM ranked r
WHERE m.id = r.id AND r.rn > 1;
-- +goose StatementEnd

-- +goose StatementBegin
-- Enforce one binding per app so a duplicate is a hard conflict at provision
-- time, not silent data loss.
CREATE UNIQUE INDEX managed_databases_app_env_var_name_uniq
    ON managed_databases (app_id, env_var_name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS managed_databases_app_env_var_name_uniq;
-- +goose StatementEnd
