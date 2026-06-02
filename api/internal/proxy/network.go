package proxy

import (
	"fmt"
	"strings"
)

// alias is the deterministic, globally-unique DNS name a service's container is
// given on the vac-edge network. Caddy dials `{alias}:{internal_port}`. Using
// `{slug}--{service}` avoids collisions when two apps both have a service named
// e.g. "app", and is stable across redeploys (unlike the `-1` container index).
func alias(slug, service string) string {
	return fmt.Sprintf("%s--%s", slug, service)
}

// routeID is the Caddy @id handle for a custom domain's route. The "vac-route-"
// prefix lets reconcile recognise and prune VAC-managed routes.
func routeID(domainID string) string {
	return routeIDPrefix + domainID
}

const routeIDPrefix = "vac-route-"

// autoRouteID is the Caddy @id handle for a derived automatic-subdomain route.
// Derived from (app, service) rather than a domain UUID because auto hosts are
// no longer stored as rows (plan 09 F1) — so reconcile can regenerate and prune
// them purely from the current app/service/base-domain state.
func autoRouteID(appID, service string) string {
	return autoRouteIDPrefix + appID + "-" + service
}

const autoRouteIDPrefix = "vac-auto-"

// isManagedRouteID reports whether a Caddy @id belongs to VAC's dynamic route
// layer (a custom-domain route or a derived auto-host route) — the routes the
// orphan prune is allowed to delete.
func isManagedRouteID(id string) bool {
	return strings.HasPrefix(id, routeIDPrefix) || strings.HasPrefix(id, autoRouteIDPrefix)
}

// controlRouteID is the reserved @id for the dashboard's own Caddy route. The
// prune sweep keeps it as long as a ControlDomain is configured — see
// pruneOrphans.
const controlRouteID = "vac-control-route"
