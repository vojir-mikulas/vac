package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Deployment is one row per deploy attempt. Times beyond TriggeredAt are
// populated as the pipeline progresses.
type Deployment struct {
	ID            string
	AppID         string
	Status        string
	TriggeredAt   time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	ComposeHash   *string
	CommitSHA     *string
	CommitMessage *string
	Error         *string
}

// CreateDeployment is the enqueue write — handler-side. The worker picks up
// the row, runs the pipeline, and writes the rest of the lifecycle fields.
func (s *Store) CreateDeployment(ctx context.Context, appID string) (Deployment, error) {
	var d Deployment
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deployments (app_id)
		VALUES ($1)
		RETURNING id, app_id, status, triggered_at, started_at, finished_at,
		          compose_hash, commit_sha, commit_message, error
	`, appID).Scan(
		&d.ID, &d.AppID, &d.Status, &d.TriggeredAt, &d.StartedAt, &d.FinishedAt,
		&d.ComposeHash, &d.CommitSHA, &d.CommitMessage, &d.Error,
	)
	return d, err
}

func (s *Store) GetDeployment(ctx context.Context, id string) (Deployment, error) {
	var d Deployment
	err := s.pool.QueryRow(ctx, `
		SELECT id, app_id, status, triggered_at, started_at, finished_at,
		       compose_hash, commit_sha, commit_message, error
		FROM deployments WHERE id = $1
	`, id).Scan(
		&d.ID, &d.AppID, &d.Status, &d.TriggeredAt, &d.StartedAt, &d.FinishedAt,
		&d.ComposeHash, &d.CommitSHA, &d.CommitMessage, &d.Error,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	return d, err
}

// ListDeploymentsForApp returns the history for an app, newest first. Cap at
// 100 rows for now — pagination will land alongside the UI.
func (s *Store) ListDeploymentsForApp(ctx context.Context, appID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, status, triggered_at, started_at, finished_at,
		       compose_hash, commit_sha, commit_message, error
		FROM deployments WHERE app_id = $1
		ORDER BY triggered_at DESC
		LIMIT 100
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(
			&d.ID, &d.AppID, &d.Status, &d.TriggeredAt, &d.StartedAt, &d.FinishedAt,
			&d.ComposeHash, &d.CommitSHA, &d.CommitMessage, &d.Error,
		); err != nil {
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
