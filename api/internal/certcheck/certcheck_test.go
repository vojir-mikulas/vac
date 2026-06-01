package certcheck

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeStore struct {
	domains  []store.DomainCert
	notAfter map[string]time.Time
	notified map[string]time.Time
	cleared  map[string]bool
	setErr   error
}

func newFakeStore(domains ...store.DomainCert) *fakeStore {
	return &fakeStore{
		domains:  domains,
		notAfter: map[string]time.Time{},
		notified: map[string]time.Time{},
		cleared:  map[string]bool{},
	}
}

func (f *fakeStore) ListDomainCerts(context.Context) ([]store.DomainCert, error) {
	return f.domains, nil
}
func (f *fakeStore) SetCertNotAfter(_ context.Context, id string, na time.Time) error {
	f.notAfter[id] = na
	return f.setErr
}
func (f *fakeStore) MarkCertExpiryNotified(_ context.Context, id string, at time.Time) error {
	f.notified[id] = at
	return nil
}
func (f *fakeStore) ClearCertExpiryNotified(_ context.Context, id string) error {
	f.cleared[id] = true
	return nil
}

type alert struct {
	host     string
	daysLeft int
}

type fakeNotifier struct{ alerts []alert }

func (n *fakeNotifier) CertExpiring(host string, daysLeft int, _ time.Time) {
	n.alerts = append(n.alerts, alert{host, daysLeft})
}

// checker builds a Checker with an injected probe (bypassing the real TLS dial).
func checker(s Store, n Notifier, probe Probe, now time.Time) *Checker {
	return &Checker{
		store:    s,
		notifier: n,
		probe:    probe,
		cfg:      Config{Threshold: 14 * 24 * time.Hour, HourOfDay: 4, InitialDelay: time.Minute},
		logger:   slog.New(slog.NewTextHandler(discard{}, nil)),
		now:      func() time.Time { return now },
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func probeReturning(m map[string]time.Time) Probe {
	return func(_ context.Context, host string) (time.Time, error) {
		na, ok := m[host]
		if !ok {
			return time.Time{}, errors.New("no cert")
		}
		return na, nil
	}
}

func TestAlertsWithinThreshold(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := newFakeStore(store.DomainCert{ID: "d1", Hostname: "app.example.com"})
	n := &fakeNotifier{}
	expiry := now.Add(5 * 24 * time.Hour) // 5 days out → within 14-day window
	c := checker(s, n, probeReturning(map[string]time.Time{"app.example.com": expiry}), now)

	if err := c.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 1 || n.alerts[0].host != "app.example.com" {
		t.Fatalf("expected one alert, got %+v", n.alerts)
	}
	if n.alerts[0].daysLeft != 5 {
		t.Errorf("days left = %d, want 5", n.alerts[0].daysLeft)
	}
	if _, ok := s.notified["d1"]; !ok {
		t.Error("expected notified stamp to be set")
	}
	if got := s.notAfter["d1"]; !got.Equal(expiry) {
		t.Errorf("not_after not recorded: %v", got)
	}
}

func TestNoAlertWhenHealthy(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := newFakeStore(store.DomainCert{ID: "d1", Hostname: "app.example.com"})
	n := &fakeNotifier{}
	expiry := now.Add(60 * 24 * time.Hour) // well outside window
	c := checker(s, n, probeReturning(map[string]time.Time{"app.example.com": expiry}), now)

	if err := c.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 0 {
		t.Errorf("expected no alert, got %+v", n.alerts)
	}
}

func TestDedupesUntilRenewed(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	already := now.Add(-time.Hour)
	// Already notified for a still-expiring cert.
	s := newFakeStore(store.DomainCert{ID: "d1", Hostname: "h", NotifiedAt: &already})
	n := &fakeNotifier{}
	expiry := now.Add(3 * 24 * time.Hour)
	c := checker(s, n, probeReturning(map[string]time.Time{"h": expiry}), now)

	if err := c.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 0 {
		t.Errorf("should not re-alert an already-notified cert, got %+v", n.alerts)
	}
}

func TestClearsStampOnRenewal(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	already := now.Add(-time.Hour)
	// Previously notified, but the cert has now been renewed (far-future expiry).
	s := newFakeStore(store.DomainCert{ID: "d1", Hostname: "h", NotifiedAt: &already})
	n := &fakeNotifier{}
	expiry := now.Add(80 * 24 * time.Hour)
	c := checker(s, n, probeReturning(map[string]time.Time{"h": expiry}), now)

	if err := c.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.cleared["d1"] {
		t.Error("expected de-dupe stamp to be cleared after renewal")
	}
	if len(n.alerts) != 0 {
		t.Errorf("renewed cert should not alert, got %+v", n.alerts)
	}
}

func TestProbeFailureSkips(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := newFakeStore(store.DomainCert{ID: "d1", Hostname: "no-cert-yet"})
	n := &fakeNotifier{}
	c := checker(s, n, probeReturning(map[string]time.Time{}), now) // probe errors

	if err := c.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 0 || len(s.notAfter) != 0 {
		t.Errorf("a failed probe should record nothing and not alert")
	}
}

func TestExpiredCertAlertsOnce(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := newFakeStore(store.DomainCert{ID: "d1", Hostname: "h"})
	n := &fakeNotifier{}
	expiry := now.Add(-2 * 24 * time.Hour) // already expired
	c := checker(s, n, probeReturning(map[string]time.Time{"h": expiry}), now)

	if err := c.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.alerts) != 1 {
		t.Fatalf("expired cert should alert, got %+v", n.alerts)
	}
	if n.alerts[0].daysLeft > 0 {
		t.Errorf("days left for expired cert should be <= 0, got %d", n.alerts[0].daysLeft)
	}
}
