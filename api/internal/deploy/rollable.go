package deploy

import (
	"github.com/vojir-mikulas/vac/api/internal/compose"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// rollable reports whether a service can be redeployed with zero downtime —
// brought up alongside the old generation, health-gated, then cut over. Only
// stateless HTTP services qualify (A3 §2). A service is rollable iff ALL hold:
//
//  1. It has an internal HTTP port — something Caddy routes to. Portless
//     workers/queues aren't user-facing; recreate them in place.
//  2. Its compose definition declares no volumes (named or bind) — stateless.
//     A volume means state that can't be duplicated for an overlap window
//     (two writers on one volume corrupts), so we never roll it.
//  3. It is a single replica — A3 v1 scope.
//
// Everything that isn't rollable is recreated in place as today (a brief blip
// on a service that usually has no public route, or is a single-writer store,
// is acceptable — there is no correct zero-downtime alternative for it).
//
// internalPort comes from the persisted service row (detected post-deploy);
// def is the parsed compose definition (volumes/replicas). A nil def means the
// service isn't in the resolved compose file (shouldn't happen for a tracked
// service) — treat as not rollable.
func rollable(internalPort *int, def *compose.Service) bool {
	if internalPort == nil {
		return false
	}
	if def == nil {
		return false
	}
	if def.HasVolumes {
		return false
	}
	if def.Replicas > 1 {
		return false
	}
	return true
}

// rollableService is the convenience form over the two records the pipeline
// already holds: the persisted service row (for internal_port) and the parsed
// compose service (for volumes/replicas).
func rollableService(svc store.Service, def *compose.Service) bool {
	return rollable(svc.InternalPort, def)
}
