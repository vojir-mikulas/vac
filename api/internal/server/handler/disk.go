package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

// DiskReporter reads the host's docker disk-usage summary. *dockercli.Compose
// satisfies it.
type DiskReporter interface {
	SystemDF(ctx context.Context) (dockercli.DiskUsage, error)
}

// DiskPruner reclaims disk by removing dangling images and unused build cache.
// *dockercli.Compose satisfies it.
type DiskPruner interface {
	PruneDanglingImages(ctx context.Context) (int64, error)
	PruneBuildCacheAll(ctx context.Context) (int64, error)
}

// DiskUsage reports the docker disk-usage breakdown (images, build cache,
// volumes, containers) for the Instance settings Maintenance card.
//
// GET /api/instance/disk
func DiskUsage(d DiskReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		du, err := d.SystemDF(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not read disk usage")
			return
		}
		WriteJSON(w, http.StatusOK, du)
	}
}

// PruneDisk removes dangling images and all reclaimable build cache, returning
// the bytes freed per category. Safe by construction: a running service's image
// is tagged and container-referenced, so `docker image prune` leaves it alone —
// only the orphaned untagged layers rebuilds leave behind are removed. Rollback
// rebuilds from the pinned commit (see deploy/pipeline.go), so it never depends
// on old images. The on-demand counterpart to the nightly retention pass.
//
// POST /api/instance/prune
func PruneDisk(d DiskPruner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		audit.Describe(r.Context(), "reclaimed disk space (pruned dangling images + build cache)")
		// Detach from the request so a client disconnect doesn't abort a prune
		// mid-flight, but bound it so a wedged daemon can't hang forever.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()

		imageBytes, err := d.PruneDanglingImages(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not prune images")
			return
		}
		cacheBytes, err := d.PruneBuildCacheAll(ctx)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not prune build cache")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int64{
			"images_reclaimed_bytes":      imageBytes,
			"build_cache_reclaimed_bytes": cacheBytes,
			"total_reclaimed_bytes":       imageBytes + cacheBytes,
		})
	}
}
