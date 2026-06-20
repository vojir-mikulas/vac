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
	// TriggerEventPreview fires a per-branch preview environment: a push to a
	// matching non-default branch creates-or-redeploys a preview app, distinct
	// from TriggerEventPush which redeploys the parent (preview-deployments.md).
	TriggerEventPreview = "preview"
)

// DeployTrigger is one push-to-deploy rule for an app: deploy when an inbound
// event of Event matches Filter (a branch/tag glob; "" matches any ref of that
// type). The matching engine and webhook endpoint land in plan 01; this is the
// schema seam so that work doesn't churn migrations.
type DeployTrigger struct {
	ID     string
	AppID  string
	Event  string
	Filter string
	// RequireApproval gates matching pushes behind manual operator approval
	// (maintenance-mode-and-deploy-gates.md, Phase 4): a matched deploy is created
	// `pending-approval` and not enqueued until approved.
	RequireApproval bool
	CreatedAt       time.Time
}

// deployTriggerColumns keeps the SELECT/RETURNING list in lockstep with scans.
const deployTriggerColumns = `id, app_id, event, filter, require_approval, created_at`

func scanDeployTrigger(row interface {
	Scan(dest ...any) error
}, t *DeployTrigger,
) error {
	return row.Scan(&t.ID, &t.AppID, &t.Event, &t.Filter, &t.RequireApproval, &t.CreatedAt)
}

// ListDeployTriggers returns an app's rules, oldest first.
func (s *Store) ListDeployTriggers(ctx context.Context, appID string) ([]DeployTrigger, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+deployTriggerColumns+`
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
		if err := scanDeployTrigger(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateDeployTrigger adds a rule for an app.
func (s *Store) CreateDeployTrigger(ctx context.Context, appID, event, filter string, requireApproval bool) (DeployTrigger, error) {
	var t DeployTrigger
	err := scanDeployTrigger(s.pool.QueryRow(ctx, `
		INSERT INTO deploy_triggers (app_id, event, filter, require_approval)
		VALUES ($1, $2, $3, $4)
		RETURNING `+deployTriggerColumns,
		appID, event, filter, requireApproval), &t)
	return t, err
}

// DeleteDeployTrigger removes a rule by id, scoped to its app so one app can't
// delete another's trigger. Returns ErrNotFound if no row matched both.
func (s *Store) DeleteDeployTrigger(ctx context.Context, appID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM deploy_triggers WHERE id = $1 AND app_id = $2`, id, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
