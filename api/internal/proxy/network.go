package proxy

import "fmt"

// alias is the deterministic, globally-unique DNS name a service's container is
// given on the vac-edge network. Caddy dials `{alias}:{internal_port}`. Using
// `{slug}--{service}` avoids collisions when two apps both have a service named
// e.g. "app", and is stable across redeploys (unlike the `-1` container index).
func alias(slug, service string) string {
	return fmt.Sprintf("%s--%s", slug, service)
}

// routeID is the Caddy @id handle for a domain's route. The "vac-route-" prefix
// lets reconcile recognise and prune VAC-managed routes.
func routeID(domainID string) string {
	return "vac-route-" + domainID
}

const routeIDPrefix = "vac-route-"
