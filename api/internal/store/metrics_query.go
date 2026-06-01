package store

import (
	"context"
	"time"
)

// This file holds read-only aggregate queries that back the Prometheus
// exposition (plan 13). They surface what VAC already persists — deployment
// history and the rolling request-metrics window — in the shapes the exporter
// labels by. No new data is collected here; see internal/promexport.

// DeployStatusCount is one (app, status, trigger) bucket with its tally, for
// the vac_deploys_total counter.
type DeployStatusCount struct {
	Slug        string
	Status      string
	TriggeredBy string
	Count       int64
}

// CountDeploymentsByStatus tallies every deployment grouped by app slug, status
// and trigger reason. Counters are derived from current rows; deployments are
// never pruned, so this is monotonic over the instance's lifetime.
func (s *Store) CountDeploymentsByStatus(ctx context.Context) ([]DeployStatusCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.slug, d.status, d.triggered_by, COUNT(*)::bigint
		FROM deployments d
		JOIN apps a ON a.id = d.app_id
		GROUP BY a.slug, d.status, d.triggered_by
		ORDER BY a.slug, d.status, d.triggered_by
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeployStatusCount
	for rows.Next() {
		var c DeployStatusCount
		if err := rows.Scan(&c.Slug, &c.Status, &c.TriggeredBy, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeployDuration is the wall-clock duration of an app's most recent completed
// deployment, for the vac_deploy_duration_seconds gauge.
type DeployDuration struct {
	Slug    string
	Seconds float64
}

// LatestDeployDurations returns, per app, the duration of its latest deployment
// that has both a start and a finish timestamp. Apps with no completed deploy
// are omitted.
func (s *Store) LatestDeployDurations(ctx context.Context) ([]DeployDuration, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (a.slug)
		       a.slug,
		       EXTRACT(EPOCH FROM (d.finished_at - d.started_at))
		FROM deployments d
		JOIN apps a ON a.id = d.app_id
		WHERE d.started_at IS NOT NULL AND d.finished_at IS NOT NULL
		ORDER BY a.slug, d.finished_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeployDuration
	for rows.Next() {
		var d DeployDuration
		if err := rows.Scan(&d.Slug, &d.Seconds); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RequestTotal is the summed request/error counts for one service over the
// retained window, for vac_requests_total / vac_request_errors_total.
type RequestTotal struct {
	Slug     string
	Service  string
	Requests int64
	Errors   int64
}

// SumRequestMetrics sums the rolling request-metrics buckets per app+service
// since `since`. Because the window is pruned (deviation D6 — stats are not kept
// forever), this counter resets as old buckets age out; the exporter documents
// that in the metric HELP.
func (s *Store) SumRequestMetrics(ctx context.Context, since time.Time) ([]RequestTotal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.slug, rm.service_name,
		       SUM(rm.requests)::bigint, SUM(rm.errors)::bigint
		FROM request_metrics rm
		JOIN apps a ON a.id = rm.app_id
		WHERE rm.bucket_ts >= $1
		GROUP BY a.slug, rm.service_name
		ORDER BY a.slug, rm.service_name
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestTotal
	for rows.Next() {
		var t RequestTotal
		if err := rows.Scan(&t.Slug, &t.Service, &t.Requests, &t.Errors); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RunningServiceRef identifies a running container for one app service, carrying
// the app slug so the stats sampler can label per-service gauges. Used by the
// scrape-time SnapshotAll (it polls `docker stats` for these container ids).
type RunningServiceRef struct {
	Slug        string
	ServiceName string
	ContainerID string
}

// ListRunningServices returns every service with a live container (running or
// degraded), joined to its app slug. Unlike the subscriber-gated live
// collectors, this is a point-in-time enumeration for one Prometheus scrape.
func (s *Store) ListRunningServices(ctx context.Context) ([]RunningServiceRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.slug, sv.service_name, sv.container_id
		FROM services sv
		JOIN apps a ON a.id = sv.app_id
		WHERE sv.container_id IS NOT NULL AND sv.container_id <> ''
		  AND sv.status IN ('running', 'degraded')
		ORDER BY a.slug, sv.service_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunningServiceRef
	for rows.Next() {
		var r RunningServiceRef
		var cid string
		if err := rows.Scan(&r.Slug, &r.ServiceName, &cid); err != nil {
			return nil, err
		}
		r.ContainerID = cid
		out = append(out, r)
	}
	return out, rows.Err()
}
