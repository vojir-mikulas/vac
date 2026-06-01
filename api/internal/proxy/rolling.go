package proxy

import (
	"context"
	"fmt"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// genAlias is the vac-edge alias for ONE generation of a rollable service
// during a zero-downtime (A3) overlap. It must differ from the bare
// {slug}--{service} alias so the old and new generation containers resolve to
// distinct addresses while both run — Caddy can then route to, and health-check,
// each independently. gen is a short, deploy-unique token (e.g. the deploy id).
func genAlias(slug, service, gen string) string {
	return fmt.Sprintf("%s--%s--%s", slug, service, gen)
}

// genDial is the upstream dial address for a service's new generation:
// {slug}--{service}--{gen}:{internal_port}.
func (m *Manager) genDial(slug string, svc store.Service, gen string) string {
	return fmt.Sprintf("%s:%d", genAlias(slug, svc.ServiceName, gen), portOr(svc.InternalPort))
}

// AttachGeneration connects a freshly-started generation container to vac-edge
// under its generation alias, so Caddy can route to it independently of the old
// generation still serving under the live alias. Mechanism-independent: the
// caller (the pipeline's rolling branch) owns how the container was created
// (M1 scale / M2 docker run — settled by the A3 spike); this only wires its
// edge attachment.
func (m *Manager) AttachGeneration(ctx context.Context, slug, service, gen, containerID string) error {
	return m.net.NetworkConnect(ctx, m.cfg.EdgeNetwork, containerID, genAlias(slug, service, gen))
}

// GateGeneration points every route of one service at BOTH the live (old) and
// the new generation upstreams, then blocks until Caddy reports the NEW upstream
// healthy. The old upstream serves the whole time — no request is ever sent only
// to the unproven new generation. Returns ErrUnhealthy if the new generation
// never goes healthy within the health budget, in which case the caller MUST NOT
// cut over (A3 §7: the old generation keeps serving, deploy → error).
func (m *Manager) GateGeneration(ctx context.Context, appID, service, gen string) error {
	app, svc, domains, err := m.serviceRoutes(ctx, appID, service)
	if err != nil {
		return err
	}
	newDial := m.genDial(app.Slug, svc, gen)
	oldDial := m.dial(app.Slug, svc)
	for _, d := range domains {
		// Old first so it remains the primary while the new one is validated.
		if err := m.caddy.PutRoute(ctx, routeID(d.ID), m.routeForDials(d, healthPathOf(svc), oldDial, newDial)); err != nil {
			return fmt.Errorf("gate route %s: %w", d.Hostname, err)
		}
	}
	// No domains → nothing to gate (shouldn't happen for a rollable service, which
	// is HTTP/routed by definition), but treat as healthy rather than block.
	return m.waitForDials(ctx, map[string]bool{newDial: true})
}

// Cutover narrows every route of a service to ONLY the new generation upstream,
// atomically per route via PutRoute (Caddy applies a route replace atomically;
// in-flight requests on the old upstream are unaffected, new requests go to the
// new alias). After this the old upstream receives no new requests — the caller
// drains it (DrainWindow) before removing the old container. The caller also
// persists services.route_alias so later Syncs keep dialing the new generation.
func (m *Manager) Cutover(ctx context.Context, appID, service, gen string) error {
	app, svc, domains, err := m.serviceRoutes(ctx, appID, service)
	if err != nil {
		return err
	}
	newDial := m.genDial(app.Slug, svc, gen)
	for _, d := range domains {
		if err := m.caddy.PutRoute(ctx, routeID(d.ID), m.routeForDials(d, healthPathOf(svc), newDial)); err != nil {
			return fmt.Errorf("cutover route %s: %w", d.Hostname, err)
		}
	}
	return nil
}

// DetachContainer removes a container from vac-edge. Used to retire the old
// generation after the drain window, or to clean up a failed new generation
// that never cut over.
func (m *Manager) DetachContainer(ctx context.Context, containerID string) error {
	return m.net.NetworkDisconnect(ctx, m.cfg.EdgeNetwork, containerID)
}

// serviceRoutes loads the app, the one service, and the domains routed to it —
// the common context the rolling cutover methods operate on.
func (m *Manager) serviceRoutes(ctx context.Context, appID, service string) (store.App, store.Service, []store.Domain, error) {
	app, err := m.store.GetApp(ctx, appID)
	if err != nil {
		return store.App{}, store.Service{}, nil, err
	}
	svc, err := m.store.GetService(ctx, appID, service)
	if err != nil {
		return store.App{}, store.Service{}, nil, err
	}
	allDomains, err := m.store.ListDomainsByApp(ctx, appID)
	if err != nil {
		return store.App{}, store.Service{}, nil, err
	}
	var domains []store.Domain
	for _, d := range allDomains {
		if d.ServiceName == service {
			domains = append(domains, d)
		}
	}
	return app, svc, domains, nil
}
