package store

import (
	"context"
	"time"
)

// Deploy trigger event types (deploy_triggers.event). Values live in Go to keep
// the set authoritative; the DB column is plain TEXT.
const (
	TriggerEventPush   = "push"   // a branch push whose ref matches Filter
	TriggerEventTag    = "tag"    // a tag push whose ref matches Filter
	TriggerEventManual = "manual" // dashboard / API button only (no auto-deploy)
)

// DeployTrigger is one push-to-deploy rule for an app: deploy when an inbound
// event of Event matches Filter (a branch/tag glob; "" matches any ref of that
// type). The matching engine and webhook endpoint land in plan 01; this is the
// schema seam so that work doesn't churn migrations.
type DeployTrigger struct {
	ID        string
	AppID     string
	Event     string
	Filter    string
	CreatedAt time.Time
}

// ListDeployTriggers returns an app's rules, oldest first.
func (s *Store) ListDeployTriggers(ctx context.Context, appID string) ([]DeployTrigger, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, event, filter, created_at
		FROM deploy_triggers WHERE app_id = $1
		ORDER BY created_at ASC
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeployTrigger
	for rows.Next() {
		var t DeployTrigger
		if err := rows.Scan(&t.ID, &t.AppID, &t.Event, &t.Filter, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateDeployTrigger adds a rule for an app.
func (s *Store) CreateDeployTrigger(ctx context.Context, appID, event, filter string) (DeployTrigger, error) {
	var t DeployTrigger
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deploy_triggers (app_id, event, filter)
		VALUES ($1, $2, $3)
		RETURNING id, app_id, event, filter, created_at
	`, appID, event, filter).Scan(&t.ID, &t.AppID, &t.Event, &t.Filter, &t.CreatedAt)
	return t, err
}

// DeleteDeployTrigger removes a rule by id. Returns ErrNotFound if no row matched.
func (s *Store) DeleteDeployTrigger(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deploy_triggers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
