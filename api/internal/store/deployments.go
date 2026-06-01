package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Trigger reasons recorded in deployments.triggered_by. Values live in Go; the
// column is plain TEXT. See migration 00020.
const (
	TriggeredManual   = "manual"   // dashboard / API button
	TriggeredPush     = "push"     // a matching git push (plan 01)
	TriggeredTag      = "tag"      // a matching git tag (plan 01)
	TriggeredRollback = "rollback" // redeploy of a prior version (plan 02)
	TriggeredSystem   = "system"   // VAC itself (future automation)
)

// Deployment is one row per deploy attempt. Times beyond TriggeredAt are
// populated as the pipeline progresses.
type Deployment struct {
	ID             string
	AppID          string
	Status         string
	TriggeredAt    time.Time
	TriggeredBy    string
	RolledBackFrom *string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	ComposeHash    *string
	CommitSHA      *string
	CommitMessage  *string
	Error          *string
}

// deploymentColumns is the canonical SELECT/RETURNING list, kept in one place
// so the field order stays in lockstep with scanDeployment.
const deploymentColumns = `id, app_id, status, triggered_at, triggered_by,
	rolled_back_from, started_at, finished_at, compose_hash, commit_sha,
	commit_message, error`

// scanDeployment scans one row in deploymentColumns order.
func scanDeployment(row pgx.Row, d *Deployment) error {
	return row.Scan(
		&d.ID, &d.AppID, &d.Status, &d.TriggeredAt, &d.TriggeredBy,
		&d.RolledBackFrom, &d.StartedAt, &d.FinishedAt, &d.ComposeHash,
		&d.CommitSHA, &d.CommitMessage, &d.Error,
	)
}

// CreateDeployment is the enqueue write — handler-side. The worker picks up
// the row, runs the pipeline, and writes the rest of the lifecycle fields.
// triggeredBy records why the deploy happened (see Triggered* constants);
// rolledBackFrom is set only for rollbacks (plan 02), nil otherwise.
func (s *Store) CreateDeployment(ctx context.Context, appID, triggeredBy string, rolledBackFrom *string) (Deployment, error) {
	if triggeredBy == "" {
		triggeredBy = TriggeredManual
	}
	var d Deployment
	err := scanDeployment(s.pool.QueryRow(ctx, `
		INSERT INTO deployments (app_id, triggered_by, rolled_back_from)
		VALUES ($1, $2, $3)
		RETURNING `+deploymentColumns,
		appID, triggeredBy, rolledBackFrom,
	), &d)
	return d, err
}

// ErrRollbackSourceInvalid is returned when a rollback names a source
// deployment that isn't a successful deploy of the same app.
var ErrRollbackSourceInvalid = errors.New("store: rollback source is not a successful deployment of this app")

// CreateRollbackDeployment enqueues a rollback: a new deployment that rebuilds
// the commit of a prior successful deployment (`sourceID`). History is
// append-only — the source row is never mutated; the new row points back at it
// via rolled_back_from and pre-seeds the source's commit SHA/message so the
// pipeline can pin the build to it (see deploy.rollbackTargetSHA).
//
// Returns ErrNotFound if the source doesn't exist and ErrRollbackSourceInvalid
// if it belongs to another app or didn't finish `running`.
func (s *Store) CreateRollbackDeployment(ctx context.Context, appID, sourceID string) (Deployment, error) {
	src, err := s.GetDeployment(ctx, sourceID)
	if err != nil {
		return Deployment{}, err
	}
	// Status literal mirrors deploy.DeploymentStatusRunning — store can't import
	// deploy (cycle); the rest of this file uses the same literals in SQL. A
	// source with no recorded commit can't be pinned (the pipeline ignores a
	// HeadCommit error, so a `running` row can still have a NULL commit_sha);
	// without the SHA a "rollback" would silently rebuild branch HEAD, so reject
	// it rather than mislabel a HEAD deploy as a rollback.
	if src.AppID != appID || src.Status != "running" || src.CommitSHA == nil {
		return Deployment{}, ErrRollbackSourceInvalid
	}
	var d Deployment
	err = scanDeployment(s.pool.QueryRow(ctx, `
		INSERT INTO deployments (app_id, triggered_by, rolled_back_from, commit_sha, commit_message)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+deploymentColumns,
		appID, TriggeredRollback, sourceID, src.CommitSHA, src.CommitMessage,
	), &d)
	return d, err
}

func (s *Store) GetDeployment(ctx context.Context, id string) (Deployment, error) {
	var d Deployment
	err := scanDeployment(s.pool.QueryRow(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE id = $1`, id), &d)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	return d, err
}

// ListDeploymentsForApp returns the history for an app, newest first. Cap at
// 100 rows for now — pagination will land alongside the UI.
func (s *Store) ListDeploymentsForApp(ctx context.Context, appID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE app_id = $1
		 ORDER BY triggered_at DESC LIMIT 100`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		var d Deployment
		if err := scanDeployment(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDeploymentStatus is called by the pipeline at each step transition.
// `errMsg` is set only on the terminal failure transition.
func (s *Store) UpdateDeploymentStatus(ctx context.Context, id, status string, errMsg *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		SET status = $2,
		    error  = COALESCE($3, error)
		WHERE id = $1
	`, id, status, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkDeploymentStarted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		SET started_at = NOW()
		WHERE id = $1 AND started_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkDeploymentFinished(ctx context.Context, id, status string, errMsg *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		SET status      = $2,
		    error       = COALESCE($3, error),
		    finished_at = NOW()
		WHERE id = $1
	`, id, status, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDeploymentCommit records the commit SHA + message once we have them
// (after the clone/pull step). Either pointer may be nil.
func (s *Store) SetDeploymentCommit(ctx context.Context, id string, sha, message *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		SET commit_sha     = COALESCE($2, commit_sha),
		    commit_message = COALESCE($3, commit_message)
		WHERE id = $1
	`, id, sha, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetDeploymentComposeHash(ctx context.Context, id, hash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE deployments SET compose_hash = $2 WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReapStuckDeployments settles deployments that have sat in a non-terminal
// state for longer than olderThan, marking them `error`. This is the periodic
// safety net complementing the boot-time sweep: it catches a row the worker
// never picked up (e.g. a crash between enqueue and start while the process
// stayed up) or a pipeline that hung with no further status transitions.
//
// Age is measured from when work began (started_at) or, for never-started
// rows, when they were queued (triggered_at). The timeout must be generous
// enough not to reap a legitimately long build; if a still-running pipeline is
// reaped, its eventual terminal write simply wins back the row (benign).
func (s *Store) ReapStuckDeployments(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		SET status      = 'error',
		    error       = COALESCE(error, 'deploy timed out — no progress for too long'),
		    finished_at = COALESCE(finished_at, NOW())
		WHERE status IN ('queued', 'cloning', 'building', 'deploying', 'health-checking')
		  AND COALESCE(started_at, triggered_at) < $1
	`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkInProgressDeploymentsInterrupted runs once at boot — any row stuck in a
// non-terminal state from a previous run becomes `interrupted`. This is the
// graceful-interrupt mechanism from mvp.md § Graceful Shutdown.
func (s *Store) MarkInProgressDeploymentsInterrupted(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments
		SET status      = 'interrupted',
		    error       = COALESCE(error, 'vac-api restarted mid-deploy'),
		    finished_at = COALESCE(finished_at, NOW())
		WHERE status IN ('queued', 'cloning', 'building', 'deploying', 'health-checking')
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
