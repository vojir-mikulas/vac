package domainstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/certprobe"
)

// fakeResolver returns canned A records and CNAMEs keyed by host.
type fakeResolver struct {
	hosts  map[string][]string
	cnames map[string]string
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	a, ok := f.hosts[host]
	if !ok || len(a) == 0 {
		return nil, errors.New("no such host")
	}
	return a, nil
}

func (f *fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if c, ok := f.cnames[host]; ok {
		return c, nil
	}
	return host, nil // no CNAME → canonical is the host itself
}

const vpsIP = "203.0.113.10"

func newEngine(r Resolver, probe certprobe.Func) *Engine {
	return New(Config{Resolver: r, VPSIP: vpsIP, CertProbe: probe})
}

// trustedProbe is a CertProbe that serves a browser-trusted cert expiring at
// notAfter. servedProbe serves an untrusted cert (e.g. staging/self-signed).
// noProbe reports no cert served (the host isn't issuing one yet).
func trustedProbe(notAfter time.Time) certprobe.Func {
	return func(context.Context, string) (certprobe.Result, error) {
		return certprobe.Result{NotAfter: notAfter, Trusted: true}, nil
	}
}

func servedProbe(notAfter time.Time) certprobe.Func {
	return func(context.Context, string) (certprobe.Result, error) {
		return certprobe.Result{NotAfter: notAfter, Trusted: false}, nil
	}
}

func noProbe(msg string) certprobe.Func {
	return func(context.Context, string) (certprobe.Result, error) {
		return certprobe.Result{}, errors.New(msg)
	}
}

func TestProbe_ApexCorrectA(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"example.com": {vpsIP}}}
	future := time.Now().Add(30 * 24 * time.Hour)
	e := newEngine(r, trustedProbe(future))
	st := e.probe(context.Background(), "example.com")
	if st.State != StateActive {
		t.Fatalf("state = %q, want active (%+v)", st.State, st)
	}
	if st.CertNotAfter == nil || !st.CertNotAfter.Equal(future) {
		t.Errorf("cert_not_after not recorded: %+v", st.CertNotAfter)
	}
}

func TestProbe_ApexWithCNAME(t *testing.T) {
	r := &fakeResolver{
		hosts:  map[string][]string{"example.com": {vpsIP}},
		cnames: map[string]string{"example.com": "something.cdn.net."},
	}
	e := newEngine(r, noProbe("x"))
	st := e.probe(context.Background(), "example.com")
	if st.State != StateMisconfigured {
		t.Fatalf("state = %q, want misconfigured", st.State)
	}
	if st.Detail == "" {
		t.Errorf("expected a CNAME-at-apex detail")
	}
}

func TestProbe_SubdomainCNAMEToBase(t *testing.T) {
	// A subdomain CNAME'd to the base host resolves (via the chain) to the VPS
	// IP — accepted.
	r := &fakeResolver{
		hosts:  map[string][]string{"app.example.com": {vpsIP}},
		cnames: map[string]string{"app.example.com": "example.com."},
	}
	future := time.Now().Add(time.Hour)
	e := newEngine(r, trustedProbe(future))
	st := e.probe(context.Background(), "app.example.com")
	if st.State != StateActive {
		t.Fatalf("state = %q, want active", st.State)
	}
}

func TestProbe_SubdomainWrongIP(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"app.example.com": {"198.51.100.5"}}}
	e := newEngine(r, noProbe("x"))
	st := e.probe(context.Background(), "app.example.com")
	if st.State != StateMisconfigured {
		t.Fatalf("state = %q, want misconfigured", st.State)
	}
}

func TestProbe_NXDOMAIN(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{}}
	e := newEngine(r, nil)
	st := e.probe(context.Background(), "nope.example.com")
	if st.State != StateAwaitingDNS {
		t.Fatalf("state = %q, want awaiting_dns", st.State)
	}
}

func TestProbe_DNSValidNoCert(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"app.example.com": {vpsIP}}}
	e := newEngine(r, noProbe("no cert yet"))
	st := e.probe(context.Background(), "app.example.com")
	if st.State != StateIssuing {
		t.Fatalf("state = %q, want issuing", st.State)
	}
}

func TestProbe_DNSValidUntrustedCert(t *testing.T) {
	// A cert is served but doesn't chain to a trusted root (staging CA or Caddy's
	// self-signed fallback). It must NOT read as active — that is the "status says
	// done but the browser rejects it" trap — and the detail should say why.
	r := &fakeResolver{hosts: map[string][]string{"app.example.com": {vpsIP}}}
	future := time.Now().Add(time.Hour)
	e := newEngine(r, servedProbe(future))
	st := e.probe(context.Background(), "app.example.com")
	if st.State != StateIssuing {
		t.Fatalf("untrusted cert ⇒ state = %q, want issuing", st.State)
	}
	if st.Detail == "" {
		t.Errorf("expected a detail explaining the untrusted cert")
	}
	if st.CertNotAfter != nil {
		t.Errorf("untrusted cert must not record cert_not_after: %+v", st.CertNotAfter)
	}
}

func TestProbe_DNSValidPastCert(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"app.example.com": {vpsIP}}}
	past := time.Now().Add(-time.Hour)
	e := newEngine(r, trustedProbe(past))
	st := e.probe(context.Background(), "app.example.com")
	if st.State != StateIssuing {
		t.Fatalf("expired cert ⇒ state = %q, want issuing", st.State)
	}
}

func TestErrorOverlayPrecedence(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"app.example.com": {vpsIP}}}
	future := time.Now().Add(time.Hour)
	e := newEngine(r, trustedProbe(future))
	// Enroll + probe → active.
	e.entries["app.example.com"] = &entry{status: e.probe(context.Background(), "app.example.com")}
	if st, _ := e.Get("app.example.com"); st.State != StateActive {
		t.Fatalf("precondition: state = %q", st.State)
	}
	// A push error overrides DNS truth.
	e.SetError("app.example.com", "boom")
	st, _ := e.Get("app.example.com")
	if st.State != StateError || st.Detail != "boom" {
		t.Fatalf("overlay not applied: %+v", st)
	}
	// Clearing restores DNS truth.
	e.ClearError("app.example.com")
	if st, _ := e.Get("app.example.com"); st.State != StateActive {
		t.Errorf("after clear, state = %q, want active", st.State)
	}
}

func TestReconcileEnrollsAndEvicts(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"a.example.com": {vpsIP}}}
	src := &fakeSource{hosts: []string{"a.example.com"}}
	e := New(Config{Resolver: r, VPSIP: vpsIP, Source: src,
		CertProbe: trustedProbe(time.Now().Add(time.Hour))})
	e.reconcile(context.Background())
	if st, ok := e.Get("a.example.com"); !ok || st.State != StateActive {
		t.Fatalf("after reconcile, a.example.com = %+v ok=%v", st, ok)
	}
	// Host vanishes from the source → evicted.
	src.hosts = []string{}
	e.reconcile(context.Background())
	if _, ok := e.Get("a.example.com"); ok {
		t.Errorf("evicted host still present")
	}
}

func TestRefreshCacheWindow(t *testing.T) {
	r := &fakeResolver{hosts: map[string][]string{"app.example.com": {vpsIP}}}
	var probeCount int
	e := New(Config{Resolver: r, VPSIP: vpsIP, CacheWindow: time.Minute,
		CertProbe: func(context.Context, string) (certprobe.Result, error) {
			probeCount++
			return certprobe.Result{NotAfter: time.Now().Add(time.Hour), Trusted: true}, nil
		}})
	// Enroll once.
	e.entries["app.example.com"] = &entry{status: e.probe(context.Background(), "app.example.com")}
	before := probeCount
	// A refresh inside the cache window must not re-probe.
	e.Refresh(context.Background(), "app.example.com")
	if probeCount != before {
		t.Errorf("refresh inside cache window re-probed (%d → %d)", before, probeCount)
	}
	// Outside the window it re-probes.
	e.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	e.Refresh(context.Background(), "app.example.com")
	if probeCount != before+1 {
		t.Errorf("refresh outside window did not re-probe (%d → %d)", before, probeCount)
	}
}

type fakeSource struct{ hosts []string }

func (f *fakeSource) StatusHosts(context.Context) ([]string, error) { return f.hosts, nil }

func TestIsApex(t *testing.T) {
	cases := map[string]bool{
		"example.com":       true,
		"app.example.com":   false,
		"example.co.uk":     true,
		"a.b.example.co.uk": false,
	}
	for host, want := range cases {
		if got := isApex(host); got != want {
			t.Errorf("isApex(%q) = %v, want %v", host, got, want)
		}
	}
}
