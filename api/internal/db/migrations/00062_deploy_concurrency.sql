-- +goose Up
-- +goose StatementBegin
-- Operator-tunable deploy concurrency (plan 20). DEFAULT 1 preserves today's
-- strictly-serial behavior; the worker pool fans out to this many concurrent
-- deploys across DIFFERENT apps. Capped at 8 — past that a single VPS thrashes
-- build I/O. The handler clamps to the same range before writing.
ALTER TABLE instance_settings
    ADD COLUMN max_concurrent_deploys INT NOT NULL DEFAULT 1
        CHECK (max_concurrent_deploys BETWEEN 1 AND 8);
-- +goose StatementEnd

-- +goose StatementBegin
-- Settle any pre-existing duplicate active deploys per app so the partial unique
-- index below can be created on existing data. Keep the newest active row per
-- app, mark the rest `interrupted`. The boot sweep marks ALL active rows
-- interrupted on every restart anyway (this migration runs at boot just before
-- it), so this only pre-empts that fate for the duplicates.
UPDATE deployments
SET status      = 'interrupted',
    error       = COALESCE(error, 'superseded — concurrent deploy for the same app'),
    finished_at = COALESCE(finished_at, NOW())
WHERE status IN ('queued','cloning','building','deploying','health-checking')
  AND id NOT IN (
      SELECT DISTINCT ON (app_id) id
      FROM deployments
      WHERE status IN ('queued','cloning','building','deploying','health-checking')
      ORDER BY app_id, triggered_at DESC
  );
-- +goose StatementEnd

-- +goose StatementBegin
-- At most one non-terminal (active OR queued) deployment per app. This makes the
-- webhook's coalescing behavior atomic and consistent across EVERY trigger path
-- (manual, rollback, webhook) — closing the check-then-insert race — and lets the
-- worker pool run >1 concurrent deploy across different apps without two workers
-- ever racing the same app's git workdir + compose stack. Terminal states
-- (running/error/interrupted/canceled) are excluded, so a settled app is
-- immediately free to deploy again.
CREATE UNIQUE INDEX one_active_deploy_per_app
    ON deployments (app_id)
    WHERE status IN ('queued','cloning','building','deploying','health-checking');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS one_active_deploy_per_app;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE instance_settings DROP COLUMN IF EXISTS max_concurrent_deploys;
-- +goose StatementEnd
