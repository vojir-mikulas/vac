// Package certcheck runs a daily background goroutine that reads the real expiry
// of every managed host's TLS certificate and fires one notification when a cert
// is within the alert window and has not auto-renewed (plan 03, deviation D7).
//
// VAC's control plane is deliberately off the vac-edge network and cannot probe
// app containers, and Caddy's admin API exposes no per-host `not_after`. So the
// expiry is read the same way a browser sees it: a TLS handshake to the proxy
// with the host's SNI, reading the served leaf certificate's NotAfter. Trust is
// irrelevant here — we only need the date — so verification is skipped.
package certcheck

import (
	"context"
	"log/slog"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/certprobe"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Store is the slice of *store.Store the checker uses.
type Store interface {
	ListDomainCerts(ctx context.Context) ([]store.DomainCert, error)
	SetCertNotAfter(ctx context.Context, id string, notAfter time.Time) error
	MarkCertExpiryNotified(ctx context.Context, id string, at time.Time) error
	ClearCertExpiryNotified(ctx context.Context, id string) error
}

// Notifier fires the expiry alert. *notify.Dispatcher satisfies it.
type Notifier interface {
	CertExpiring(host string, daysLeft int, notAfter time.Time)
}

// Probe reads the leaf certificate's NotAfter for a host. Injectable so tests
// avoid real TLS. A probe error (host unreachable, no cert yet) is non-fatal —
// the checker skips that host this round. Shared with domainstatus via the
// certprobe package (plan 09 §4).
type Probe = certprobe.Func

// Config parameterises the checker.
type Config struct {
	// Threshold is the alert window: a cert expiring within it (and not renewed)
	// fires once. Default 14 days.
	Threshold time.Duration
	// HourOfDay (0-23, time.Local) the daily check runs. Default 4 (04:00) —
	// after the 03:00 retention prune.
	HourOfDay int
	// InitialDelay is how long to wait after boot before the first check, giving
	// the proxy time to come up. Default 2m.
	InitialDelay time.Duration
}

// Checker is the background scheduler.
type Checker struct {
	store    Store
	notifier Notifier
	probe    Probe
	cfg      Config
	logger   *slog.Logger
	now      func() time.Time
}

// New wires a Checker. proxyAddr is the host:port to TLS-dial with per-host SNI
// (e.g. "vac-proxy:443"). A nil notifier disables alerting (the checker still
// records observed expiry). dialTimeout bounds each handshake (default 10s).
func New(s Store, notifier Notifier, proxyAddr string, dialTimeout time.Duration, cfg Config, logger *slog.Logger) *Checker {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 14 * 24 * time.Hour
	}
	if cfg.HourOfDay < 0 || cfg.HourOfDay > 23 {
		cfg.HourOfDay = 4
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 2 * time.Minute
	}
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	return &Checker{
		store:    s,
		notifier: notifier,
		probe:    certprobe.New(proxyAddr, dialTimeout),
		cfg:      cfg,
		logger:   logger,
		now:      time.Now,
	}
}

// Run does an initial check after InitialDelay, then once per day at HourOfDay.
// Exits when ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(c.cfg.InitialDelay):
	}
	if err := c.CheckOnce(ctx); err != nil {
		c.logger.Warn("certcheck: initial check failed", "err", err)
	}
	for {
		wait := timeUntilNext(c.now(), c.cfg.HourOfDay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := c.CheckOnce(ctx); err != nil {
			c.logger.Warn("certcheck: check failed", "err", err)
		}
	}
}

// CheckOnce probes every managed host once, records the observed expiry, and
// alerts (once per threshold crossing) on near-expiry certs. Exposed for tests.
func (c *Checker) CheckOnce(ctx context.Context) error {
	domains, err := c.store.ListDomainCerts(ctx)
	if err != nil {
		return err
	}
	now := c.now()
	for _, d := range domains {
		notAfter, err := c.probe(ctx, d.Hostname)
		if err != nil {
			// A host without a cert yet, or briefly unreachable, is normal — keep
			// it at debug so the log isn't noisy.
			c.logger.Debug("certcheck: probe failed", "host", d.Hostname, "err", err)
			continue
		}
		if err := c.store.SetCertNotAfter(ctx, d.ID, notAfter); err != nil {
			c.logger.Warn("certcheck: record expiry", "host", d.Hostname, "err", err)
		}

		remaining := notAfter.Sub(now)
		if remaining > c.cfg.Threshold {
			// Healthy / renewed — reset the de-dupe stamp so a future approach to
			// expiry alerts afresh.
			if d.NotifiedAt != nil {
				if err := c.store.ClearCertExpiryNotified(ctx, d.ID); err != nil {
					c.logger.Warn("certcheck: clear notified", "host", d.Hostname, "err", err)
				}
			}
			continue
		}
		// Within the alert window. Fire once per crossing.
		if d.NotifiedAt != nil {
			continue
		}
		daysLeft := int(remaining / (24 * time.Hour))
		if c.notifier != nil {
			c.notifier.CertExpiring(d.Hostname, daysLeft, notAfter)
		}
		if err := c.store.MarkCertExpiryNotified(ctx, d.ID, now); err != nil {
			c.logger.Warn("certcheck: mark notified", "host", d.Hostname, "err", err)
		}
		c.logger.Info("certcheck: cert expiring soon", "host", d.Hostname, "days_left", daysLeft, "not_after", notAfter.Format(time.RFC3339))
	}
	return nil
}

func timeUntilNext(now time.Time, hour int) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}
