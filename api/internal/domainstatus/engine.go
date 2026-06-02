package domainstatus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/certprobe"
)

// HostSource enumerates the hostnames the engine watches: every custom domain
// plus every derived auto host (plan 09 F1). Hosts that vanish from the set are
// evicted from the projection.
type HostSource interface {
	StatusHosts(ctx context.Context) ([]string, error)
}

// Config parameterises the engine.
type Config struct {
	Source    HostSource
	Resolver  Resolver       // DNS reads; defaults to the system resolver
	CertProbe certprobe.Func // "is a leaf served right now"; nil ⇒ never reaches active
	VPSIP     string         // expected A-record target; "" disables the IP-match check

	DNSTimeout      time.Duration // per DNS lookup; default 4s
	CertTimeout     time.Duration // per TLS probe; default 10s
	Concurrency     int           // bounded probe workers; default 6
	ActiveInterval  time.Duration // re-check cadence for active hosts; default 5m
	PendingInterval time.Duration // re-check cadence for non-active hosts; default 30s
	CacheWindow     time.Duration // min spacing for a forced Refresh; default 15s
	Tick            time.Duration // base reconcile loop interval; default 15s
	InitialDelay    time.Duration // wait after boot before first pass; default 5s

	Logger *slog.Logger
}

type entry struct {
	status Status
}

// Engine is the background reconciler + in-memory status store.
type Engine struct {
	cfg     Config
	logger  *slog.Logger
	now     func() time.Time
	mu      sync.RWMutex
	entries map[string]*entry
	pushErr map[string]string // overlay set by the proxy manager (plan 09 F3 §3 step 4)
}

// New wires an Engine, applying defaults.
func New(cfg Config) *Engine {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Resolver == nil {
		cfg.Resolver = PublicResolver("")
	}
	if cfg.DNSTimeout <= 0 {
		cfg.DNSTimeout = 4 * time.Second
	}
	if cfg.CertTimeout <= 0 {
		cfg.CertTimeout = 10 * time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 6
	}
	if cfg.ActiveInterval <= 0 {
		cfg.ActiveInterval = 5 * time.Minute
	}
	if cfg.PendingInterval <= 0 {
		cfg.PendingInterval = 30 * time.Second
	}
	if cfg.CacheWindow <= 0 {
		cfg.CacheWindow = 15 * time.Second
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 15 * time.Second
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 5 * time.Second
	}
	return &Engine{
		cfg:     cfg,
		logger:  cfg.Logger,
		now:     time.Now,
		entries: map[string]*entry{},
		pushErr: map[string]string{},
	}
}

// Run reconciles after InitialDelay (the proxy must be up to probe certs), then
// every Tick. Exits on ctx cancel.
func (e *Engine) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(e.cfg.InitialDelay):
	}
	e.reconcile(ctx)
	ticker := time.NewTicker(e.cfg.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reconcile(ctx)
		}
	}
}

// reconcile refreshes the host set, evicts stale hosts, and re-probes any host
// whose tiered cadence is due.
func (e *Engine) reconcile(ctx context.Context) {
	hosts, err := e.cfg.Source.StatusHosts(ctx)
	if err != nil {
		e.logger.Warn("domainstatus: enumerate hosts", "err", err)
		return
	}
	set := make(map[string]bool, len(hosts))
	var due []string
	e.mu.Lock()
	for _, h := range hosts {
		h = strings.ToLower(h)
		set[h] = true
		if _, ok := e.entries[h]; !ok {
			e.entries[h] = &entry{status: Status{State: StateChecking}}
		}
	}
	for h := range e.entries {
		if !set[h] {
			delete(e.entries, h)
			delete(e.pushErr, h)
		}
	}
	for h, ent := range e.entries {
		if e.due(ent) {
			due = append(due, h)
		}
	}
	e.mu.Unlock()

	e.probeAll(ctx, due)
}

// due reports whether an entry is past its tiered re-check interval.
func (e *Engine) due(ent *entry) bool {
	if ent.status.LastChecked == nil {
		return true
	}
	interval := e.cfg.PendingInterval
	if ent.status.State == StateActive {
		interval = e.cfg.ActiveInterval
	}
	return e.now().Sub(*ent.status.LastChecked) >= interval
}

// probeAll probes the given hosts with bounded concurrency.
func (e *Engine) probeAll(ctx context.Context, hosts []string) {
	if len(hosts) == 0 {
		return
	}
	sem := make(chan struct{}, e.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(host string) {
			defer wg.Done()
			defer func() { <-sem }()
			st := e.probe(ctx, host)
			e.store(host, st)
		}(h)
	}
	wg.Wait()
}

// probe resolves and (if DNS is valid) cert-probes one host, returning its
// derived status. `error` is never inferred here — it is the proxy manager's
// push-failure overlay (see Get / SetError).
func (e *Engine) probe(ctx context.Context, host string) Status {
	now := e.now()
	st := Status{LastChecked: &now}

	dctx, cancel := context.WithTimeout(ctx, e.cfg.DNSTimeout)
	addrs, err := e.cfg.Resolver.LookupHost(dctx, host)
	cancel()
	if err != nil || len(addrs) == 0 {
		st.State = StateAwaitingDNS
		return st
	}

	if isApex(host) {
		cctx, cancel := context.WithTimeout(ctx, e.cfg.DNSTimeout)
		cname, cerr := e.cfg.Resolver.LookupCNAME(cctx, host)
		cancel()
		if cerr == nil && cname != "" && !strings.EqualFold(strings.TrimSuffix(cname, "."), host) {
			st.State = StateMisconfigured
			st.Detail = fmt.Sprintf("CNAME at apex is invalid — use an A record to %s", e.cfg.VPSIP)
			return st
		}
	}

	if e.cfg.VPSIP != "" && !contains(addrs, e.cfg.VPSIP) {
		st.State = StateMisconfigured
		st.Detail = fmt.Sprintf("resolves to %s — expected %s", strings.Join(addrs, ", "), e.cfg.VPSIP)
		return st
	}

	// DNS valid → is a leaf cert being served right now?
	if e.cfg.CertProbe != nil {
		pctx, cancel := context.WithTimeout(ctx, e.cfg.CertTimeout)
		notAfter, perr := e.cfg.CertProbe(pctx, host)
		cancel()
		if perr == nil && notAfter.After(now) {
			st.State = StateActive
			na := notAfter
			st.CertNotAfter = &na
			return st
		}
	}
	st.State = StateIssuing
	return st
}

// store records a fresh probe result, unless the host was evicted meanwhile.
func (e *Engine) store(host string, st Status) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ent, ok := e.entries[host]; ok {
		ent.status = st
	}
}

// Get returns the projected status for a host. The proxy manager's push-error
// overlay takes precedence over DNS/cert truth (and vice-versa): a route that
// failed to push reads `error` regardless of DNS, until the push succeeds.
func (e *Engine) Get(host string) (Status, bool) {
	host = strings.ToLower(host)
	e.mu.RLock()
	defer e.mu.RUnlock()
	ent, ok := e.entries[host]
	if !ok {
		// A host the manager flagged with an error but the engine hasn't enrolled
		// yet still reports error.
		if d := e.pushErr[host]; d != "" {
			return Status{State: StateError, Detail: d}, true
		}
		return Status{}, false
	}
	st := ent.status
	if d := e.pushErr[host]; d != "" {
		st.State = StateError
		st.Detail = d
	}
	return st, true
}

// Refresh forces a re-probe of one host and returns its fresh status. Inside the
// cache window the cached result is returned instead, so an impatient operator
// can't hammer DNS (plan 09 F3 §5). Returns false for an unknown host.
func (e *Engine) Refresh(ctx context.Context, host string) (Status, bool) {
	host = strings.ToLower(host)
	e.mu.RLock()
	ent, ok := e.entries[host]
	var last *time.Time
	if ok {
		last = ent.status.LastChecked
	}
	e.mu.RUnlock()
	if !ok {
		return Status{}, false
	}
	if last != nil && e.now().Sub(*last) < e.cfg.CacheWindow {
		return e.Get(host)
	}
	e.store(host, e.probe(ctx, host))
	return e.Get(host)
}

// SetError records a route-push failure for a host so it surfaces as `error`.
// Satisfies proxy.StatusEngine.
func (e *Engine) SetError(host, detail string) {
	host = strings.ToLower(host)
	e.mu.Lock()
	e.pushErr[host] = detail
	e.mu.Unlock()
}

// ClearError clears a host's push-error overlay (the route landed). Satisfies
// proxy.StatusEngine.
func (e *Engine) ClearError(host string) {
	host = strings.ToLower(host)
	e.mu.Lock()
	delete(e.pushErr, host)
	e.mu.Unlock()
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
