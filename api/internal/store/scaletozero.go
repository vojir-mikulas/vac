package store

import (
	"context"
	"time"
)

// Scale-to-zero persistence (docs/plans/scale-to-zero.md). The runtime suspend
// flag and per-app opt-in live on the apps row; idle detection reads
// request_metrics (see LastTrafficSince in request_metrics.go).

// SetAppSuspended flips the runtime suspend flag. The sweeper sets it true after
// stopping an idle stack; the waker — and any deploy (deploy-wins) — clears it.
// updated_at moves so listings reflect the change.
func (s *Store) SetAppSuspended(ctx context.Context, id string, suspended bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE apps SET suspended = $2, updated_at = NOW() WHERE id = $1`, id, suspended)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetIdleSuspendConfig sets the per-app opt-in and optional timeout override.
// timeoutMinutes nil clears the override → the instance default (VAC_IDLE_TIMEOUT)
// applies.
func (s *Store) SetIdleSuspendConfig(ctx context.Context, id string, enabled bool, timeoutMinutes *int) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE apps SET idle_suspend_enabled = $2, idle_timeout_minutes = $3, updated_at = NOW()
		WHERE id = $1
	`, id, enabled, timeoutMinutes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetLastTrafficAt stamps the denormalized last-seen request time. The sweeper
// writes it from the request_metrics MAX so the UI can show "last active"
// without re-querying the metrics table. Advisory only — never load-bearing.
func (s *Store) SetLastTrafficAt(ctx context.Context, id string, ts time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE apps SET last_traffic_at = $2 WHERE id = $1`, id, ts)
	return err
}

// idleSuspendEligible is the shared WHERE clause for the sweeper's candidate set.
// It encodes the auto-exclusions from the plan: only opted-in, currently-running,
// non-suspended, non-preview, non-template apps with no enabled scheduled job and
// no enabled backup config are eligible. The remaining exclusion — "no routable
// HTTP service" — can't be expressed in SQL; the sweeper relies on WaitHealthy's
// empty-want signal for that.
const idleSuspendEligible = `
	idle_suspend_enabled = TRUE
	AND suspended = FALSE
	AND status = 'running'
	AND is_preview = FALSE
	AND source <> 'template'
	AND NOT EXISTS (SELECT 1 FROM scheduled_jobs j WHERE j.app_id = apps.id AND j.enabled = TRUE)
	AND NOT EXISTS (SELECT 1 FROM backup_configs b WHERE b.app_id = apps.id AND b.enabled = TRUE)`

// CountIdleSuspendApps counts apps that have opted into idle-suspend at all
// (ignoring runtime state). main.go uses it to decide whether to start the
// sweeper goroutine — zero opted-in apps → no goroutine → zero idle cost.
func (s *Store) CountIdleSuspendApps(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM apps WHERE idle_suspend_enabled = TRUE`).Scan(&n)
	return n, err
}

// ListIdleSuspendApps returns the sweeper's candidate set: apps eligible for
// suspension right now (see idleSuspendEligible). The sweeper still checks
// last-traffic per app before acting.
func (s *Store) ListIdleSuspendApps(ctx context.Context) ([]App, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+appColumns+` FROM apps WHERE `+idleSuspendEligible)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		var a App
		if err := scanApp(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
