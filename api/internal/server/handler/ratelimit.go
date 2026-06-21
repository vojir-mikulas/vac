package handler

import (
	"encoding/json"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Per-app edge rate limiting. The operator sets a requests-per-minute-per-IP cap
// that the proxy enforces at Caddy (via the caddy-ratelimit handler baked into
// the vac-proxy image). Changing it re-syncs the proxy so the limit reaches
// every host route immediately.

// maxRateLimitRPM caps the per-app limit so a typo can't push an unusable value
// into Caddy. 1,000,000 req/min is far above any single-VPS workload.
const maxRateLimitRPM = 1_000_000

type rateLimitDTO struct {
	RPM *int `json:"rpm"`
}

// GetRateLimit returns the app's edge rate limit (null rpm = no limit).
func GetRateLimit(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		WriteJSON(w, http.StatusOK, rateLimitDTO{RPM: app.RateLimitRPM})
	}
}

type setRateLimitRequest struct {
	RPM *int `json:"rpm"`
}

// SetRateLimit sets or clears the per-app limit, then re-syncs the proxy. A nil
// or non-positive rpm clears the limit; a value above the cap is clamped.
func SetRateLimit(s *store.Store, pm ProxyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		app, err := loadApp(w, r, s)
		if err != nil {
			return
		}
		var req setRateLimitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		var rpm *int
		if req.RPM != nil && *req.RPM > 0 {
			v := *req.RPM
			if v > maxRateLimitRPM {
				v = maxRateLimitRPM
			}
			rpm = &v
		}
		if err := s.SetAppRateLimit(r.Context(), app.ID, rpm); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not update rate limit")
			return
		}
		// Re-sync so Caddy adds/removes the rate_limit handler on every host route.
		proxySync(r.Context(), pm, app.ID)
		audit.SetTarget(r.Context(), "app", app.ID)
		if rpm != nil {
			audit.Action(r.Context(), "ratelimit.set", nil)
			audit.SetMetadata(r.Context(), map[string]any{"rpm": *rpm})
		} else {
			audit.Action(r.Context(), "ratelimit.cleared", nil)
		}
		WriteJSON(w, http.StatusOK, rateLimitDTO{RPM: rpm})
	}
}
