package proxy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrUnhealthy is returned by WaitHealthy when one or more of an app's HTTP
// upstreams never report healthy within the budget.
var ErrUnhealthy = errors.New("proxy: upstream did not become healthy")

// WaitHealthy gates a deploy on Caddy's view of the app's upstreams. Because
// vac-api is deliberately not on vac-edge, it can't probe containers directly;
// instead it polls Caddy's /reverse_proxy/upstreams until every routable HTTP
// service for the app appears with zero recent fails, or the budget expires.
//
// Services without a domain (no route) are not gated — they're workers/DBs.
func (m *Manager) WaitHealthy(ctx context.Context, appID string) error {
	app, err := m.store.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	domains, err := m.store.ListDomainsByApp(ctx, appID)
	if err != nil {
		return err
	}
	services, err := m.store.ListServicesForApp(ctx, appID)
	if err != nil {
		return err
	}
	byName := make(map[string]store.Service, len(services))
	for _, s := range services {
		byName[s.ServiceName] = s
	}

	// Desired dial addresses: one per service backing a desired route (assigned
	// custom domain or derived auto host) that has a container + internal port.
	// Dedup so a multi-route service is checked once.
	want := make(map[string]bool)
	for _, spec := range m.desiredRoutes(app, domains, services) {
		svc, ok := byName[spec.service]
		if ok && svc.ContainerID != nil && *svc.ContainerID != "" && svc.InternalPort != nil {
			want[m.dial(app.Slug, svc)] = true
		}
	}
	if len(want) == 0 {
		return nil // nothing routable to gate on
	}

	retries := m.cfg.HealthRetries
	if retries < 1 {
		retries = 1
	}
	interval := m.cfg.HealthTimeout / time.Duration(retries)
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Now().Add(m.cfg.HealthTimeout)

	// Sleep one interval *before* each read rather than after. An upstream
	// appears in /reverse_proxy/upstreams with fails==0 the instant its route is
	// installed — present in the pool, but not yet probed by Caddy's active
	// health checker. Reading immediately (the old attempt-0 behaviour) would gate
	// the deploy to `running` on that unverified zero: a container that merely
	// accepts a TCP connection but is still booting or serving 500s passes. By
	// waiting first, every acceptance reflects at least one completed active probe
	// (interval == HealthTimeout/retries, normally ≥ the configured
	// HealthInterval, so the checker has run by the time we trust its verdict).
	for attempt := 0; ; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if healthy, err := m.allHealthy(ctx, want); err == nil && healthy {
			return nil
		}
		if attempt+1 >= retries || time.Now().After(deadline) {
			break
		}
	}
	return fmt.Errorf("%w: %s", ErrUnhealthy, joinKeys(want))
}

// allHealthy reports whether every wanted dial address is present in Caddy's
// upstream pool with zero fails.
func (m *Manager) allHealthy(ctx context.Context, want map[string]bool) (bool, error) {
	ups, err := m.caddy.Upstreams(ctx)
	if err != nil {
		return false, err
	}
	healthy := make(map[string]bool, len(ups))
	for _, u := range ups {
		if u.Fails == 0 {
			healthy[u.Address] = true
		}
	}
	for addr := range want {
		if !healthy[addr] {
			return false, nil
		}
	}
	return true, nil
}

func joinKeys(m map[string]bool) string {
	out := ""
	for k := range m {
		if out != "" {
			out += ", "
		}
		out += k
	}
	return out
}
