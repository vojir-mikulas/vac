package store

import (
	"context"
	"time"
)

// VolumeUsage is the latest sampled disk usage of one container mount, written by
// the diskusage.Collector. UsedBytes is nil when the mount has not been measured
// yet (e.g. a bind-mount `du` that was skipped or timed out) so the UI can be
// honest about staleness rather than rendering a false 0.
type VolumeUsage struct {
	AppID       string
	ServiceName string
	VolumeName  string // empty for bind mounts
	MountPath   string
	Source      string // "named" | "bind"
	UsedBytes   *int64
	SampledAt   time.Time
	// AppSlug is populated only by ListVolumeUsage (the box-wide query joins apps)
	// for the Prometheus exporter, which labels by slug. Empty on the per-app read.
	AppSlug string
}

// UpsertVolumeUsage records the latest usage sample for a mount, keyed on
// (app_id, service_name, mount_path). A re-sample of the same mount overwrites
// the prior row, so the table holds the current snapshot, not history.
func (s *Store) UpsertVolumeUsage(ctx context.Context, v VolumeUsage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO volume_usage (app_id, service_name, volume_name, mount_path, source, used_bytes, sampled_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (app_id, service_name, mount_path) DO UPDATE
			SET volume_name = EXCLUDED.volume_name,
			    source      = EXCLUDED.source,
			    used_bytes  = EXCLUDED.used_bytes,
			    sampled_at  = EXCLUDED.sampled_at
	`, v.AppID, v.ServiceName, v.VolumeName, v.MountPath, v.Source, v.UsedBytes)
	return err
}

// DeleteVolumeUsageForAppExcept prunes rows for mounts that no longer exist on an
// app — volumes the operator removed between deploys. keepMountPaths is the set of
// mount paths seen in the current collection; an empty slice (app has no volumes
// anymore) deletes every row for the app, because `= ANY('{}')` is always false.
func (s *Store) DeleteVolumeUsageForAppExcept(ctx context.Context, appID string, keepMountPaths []string) error {
	if keepMountPaths == nil {
		keepMountPaths = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		DELETE FROM volume_usage WHERE app_id = $1 AND NOT (mount_path = ANY($2))
	`, appID, keepMountPaths)
	return err
}

// ListVolumeUsageByApp returns the latest usage sample per mount for one app,
// ordered for stable rendering. Powers GET /api/apps/{id}/volumes.
func (s *Store) ListVolumeUsageByApp(ctx context.Context, appID string) ([]VolumeUsage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT app_id, service_name, volume_name, mount_path, source, used_bytes, sampled_at
		FROM volume_usage WHERE app_id = $1
		ORDER BY service_name, mount_path
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VolumeUsage
	for rows.Next() {
		var v VolumeUsage
		if err := rows.Scan(&v.AppID, &v.ServiceName, &v.VolumeName, &v.MountPath, &v.Source, &v.UsedBytes, &v.SampledAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListVolumeUsage returns every volume sample box-wide, joined to the app slug so
// the Prometheus exporter can label vac_app_volume_bytes by app. Used for the
// /metrics scrape (and a future fleet-wide storage view).
func (s *Store) ListVolumeUsage(ctx context.Context) ([]VolumeUsage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT vu.app_id, a.slug, vu.service_name, vu.volume_name, vu.mount_path, vu.source, vu.used_bytes, vu.sampled_at
		FROM volume_usage vu
		JOIN apps a ON a.id = vu.app_id
		ORDER BY a.slug, vu.service_name, vu.mount_path
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VolumeUsage
	for rows.Next() {
		var v VolumeUsage
		if err := rows.Scan(&v.AppID, &v.AppSlug, &v.ServiceName, &v.VolumeName, &v.MountPath, &v.Source, &v.UsedBytes, &v.SampledAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
