package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/promexport"
	"github.com/vojir-mikulas/vac/api/internal/stats"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// StatsProvider is the host + per-service stats surface the Prometheus
// exposition reads. *stats.Manager satisfies it (and HostStatsProvider).
type StatsProvider interface {
	HostStatsProvider
	SnapshotAll(ctx context.Context) []stats.AppSample
}

// MetricsStore supplies the persisted aggregates (deployments + requests) the
// exposition surfaces. *store.Store satisfies it.
type MetricsStore interface {
	CountDeploymentsByStatus(ctx context.Context) ([]store.DeployStatusCount, error)
	LatestDeployDurations(ctx context.Context) ([]store.DeployDuration, error)
	SumRequestMetrics(ctx context.Context, since time.Time) ([]store.RequestTotal, error)
	ListVolumeUsage(ctx context.Context) ([]store.VolumeUsage, error)
}

// MetricsExposition renders VAC's metrics in Prometheus text format. It gathers
// host vitals, a one-shot per-service stats poll, and the deploy/request
// aggregates, then formats them via promexport. A failure in any persisted
// aggregate degrades that section to empty rather than failing the whole scrape.
//
// The route is mounted behind the metrics-token guard (default-closed), so this
// handler is only reached with a valid bearer token.
func MetricsExposition(sp StatsProvider, s MetricsStore, window time.Duration, version, commit string) http.HandlerFunc {
	if window <= 0 {
		window = 24 * time.Hour
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		snap := promexport.Snapshot{
			Host:  toHostVitals(sp.Snapshot(ctx)),
			Apps:  toAppSamples(sp.SnapshotAll(ctx)),
			Build: promexport.BuildInfo{Version: version, Commit: commit},
		}
		if counts, err := s.CountDeploymentsByStatus(ctx); err == nil {
			for _, c := range counts {
				snap.Deploys = append(snap.Deploys, promexport.DeployCount{
					App: c.Slug, Status: c.Status, TriggeredBy: c.TriggeredBy, Count: c.Count,
				})
			}
		}
		if durs, err := s.LatestDeployDurations(ctx); err == nil {
			for _, d := range durs {
				snap.DeployDurs = append(snap.DeployDurs, promexport.DeployDuration{App: d.Slug, Seconds: d.Seconds})
			}
		}
		if reqs, err := s.SumRequestMetrics(ctx, time.Now().Add(-window)); err == nil {
			for _, t := range reqs {
				snap.Requests = append(snap.Requests, promexport.RequestTotal{
					App: t.Slug, Service: t.Service, Requests: t.Requests, Errors: t.Errors,
				})
			}
		}
		if vols, err := s.ListVolumeUsage(ctx); err == nil {
			for _, v := range vols {
				if v.UsedBytes == nil {
					continue // not yet measured — don't emit a false 0
				}
				snap.Volumes = append(snap.Volumes, promexport.VolumeSample{
					App: v.AppSlug, Service: v.ServiceName, Volume: v.VolumeName, Bytes: *v.UsedBytes,
				})
			}
		}
		w.Header().Set("Content-Type", promexport.ContentType)
		w.WriteHeader(http.StatusOK)
		promexport.Write(w, snap)
	}
}

func toHostVitals(h stats.HostSnapshot) promexport.HostVitals {
	return promexport.HostVitals{
		CPUPercent:     h.CPUPercent,
		MemUsedBytes:   h.MemUsedBytes,
		MemTotalBytes:  h.MemTotalBytes,
		DiskUsedBytes:  h.DiskUsedBytes,
		DiskTotalBytes: h.DiskTotalBytes,
		RequestRate:    h.RequestRate,
	}
}

func toAppSamples(in []stats.AppSample) []promexport.AppSample {
	if len(in) == 0 {
		return nil
	}
	out := make([]promexport.AppSample, 0, len(in))
	for _, a := range in {
		out = append(out, promexport.AppSample{
			App: a.App, Service: a.Service, CPUPercent: a.CPUPercent, MemBytes: a.MemBytes,
		})
	}
	return out
}
