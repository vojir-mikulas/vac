package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestBucket is one pre-aggregated 10s window for a service, written by the
// access-log aggregator.
type RequestBucket struct {
	AppID       string
	ServiceName string
	BucketTS    time.Time
	Requests    int
	Errors      int
	BytesOut    int64
}

// RequestPoint is one point of the request-rate series returned to the UI.
type RequestPoint struct {
	TS       time.Time `json:"ts"`
	Requests int       `json:"requests"`
	Errors   int       `json:"errors"`
	BytesOut int64     `json:"bytes_out"`
}

// UpsertRequestBuckets adds the buckets, incrementing counters on conflict so a
// late log line for an already-flushed bucket accumulates rather than
// duplicates. Uses a batch for one round-trip.
func (s *Store) UpsertRequestBuckets(ctx context.Context, buckets []RequestBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, b := range buckets {
		batch.Queue(`
			INSERT INTO request_metrics (app_id, service_name, bucket_ts, requests, errors, bytes_out)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (app_id, service_name, bucket_ts) DO UPDATE
				SET requests  = request_metrics.requests + EXCLUDED.requests,
				    errors    = request_metrics.errors + EXCLUDED.errors,
				    bytes_out = request_metrics.bytes_out + EXCLUDED.bytes_out
		`, b.AppID, b.ServiceName, b.BucketTS, b.Requests, b.Errors, b.BytesOut)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()
	for range buckets {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// QueryRequestSeries returns the per-bucket series since `since`. When service
// is empty the buckets are summed across all of the app's services.
func (s *Store) QueryRequestSeries(ctx context.Context, appID, service string, since time.Time) ([]RequestPoint, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if service == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT bucket_ts, SUM(requests)::int, SUM(errors)::int, SUM(bytes_out)::bigint
			FROM request_metrics
			WHERE app_id = $1 AND bucket_ts >= $2
			GROUP BY bucket_ts
			ORDER BY bucket_ts
		`, appID, since)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT bucket_ts, requests, errors, bytes_out
			FROM request_metrics
			WHERE app_id = $1 AND service_name = $3 AND bucket_ts >= $2
			ORDER BY bucket_ts
		`, appID, since, service)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestPoint
	for rows.Next() {
		var p RequestPoint
		if err := rows.Scan(&p.TS, &p.Requests, &p.Errors, &p.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// QueryHostRequestSeries returns the request series summed across every app,
// downsampled to fixed-width buckets of `bucketSeconds` so a wide window (e.g.
// 24h of 10s buckets) collapses to a handful of points fit for a sparkline.
// Each row's ts is the start of its downsampled bucket.
func (s *Store) QueryHostRequestSeries(ctx context.Context, since time.Time, bucketSeconds int) ([]RequestPoint, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	rows, err := s.pool.Query(ctx, `
		SELECT to_timestamp(floor(extract(epoch FROM bucket_ts) / $2) * $2) AT TIME ZONE 'UTC' AS b,
		       SUM(requests)::int, SUM(errors)::int, SUM(bytes_out)::bigint
		FROM request_metrics
		WHERE bucket_ts >= $1
		GROUP BY b
		ORDER BY b
	`, since, bucketSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestPoint
	for rows.Next() {
		var p RequestPoint
		if err := rows.Scan(&p.TS, &p.Requests, &p.Errors, &p.BytesOut); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LastTrafficSince returns the most recent request-bucket timestamp for an app,
// or the zero time if it has no rows in the retained window. The idle sweeper
// compares this against now-timeout to decide suspension; reading MAX(bucket_ts)
// means idle detection needs no per-request writes (scale-to-zero decision #5).
func (s *Store) LastTrafficSince(ctx context.Context, appID string) (time.Time, error) {
	var ts *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT MAX(bucket_ts) FROM request_metrics WHERE app_id = $1`, appID).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if ts == nil {
		return time.Time{}, nil
	}
	return *ts, nil
}

// DeleteRequestMetricsOlderThan prunes the rolling window. Called by the
// retention pruner.
func (s *Store) DeleteRequestMetricsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM request_metrics WHERE bucket_ts < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
