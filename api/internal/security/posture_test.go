package security

import (
	"context"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakePostureStore struct {
	apps     []store.App
	services map[string][]store.Service // keyed by app ID
	appsErr  error
}

func (f *fakePostureStore) ListApps(_ context.Context) ([]store.App, error) {
	return f.apps, f.appsErr
}

func (f *fakePostureStore) ListServicesForApp(_ context.Context, appID string) ([]store.Service, error) {
	return f.services[appID], nil
}

// find returns the first finding with the given code.
func find(findings []PostureFinding, code string) (PostureFinding, bool) {
	for _, f := range findings {
		if f.Code == code {
			return f, true
		}
	}
	return PostureFinding{}, false
}

func TestPosture_MasterKeyAndExposure(t *testing.T) {
	st := &fakePostureStore{}

	// Healthy config: master key present, public exposure, metrics token set.
	good := NewPosture(st, nil, PostureConfig{Exposure: "public", MasterKeyPresent: true, MetricsTokenSet: true})
	g := good.Check(context.Background())
	if f, _ := find(g, "master_key_present"); f.Severity != SeverityOK {
		t.Errorf("master key OK expected, got %v", f.Severity)
	}
	if f, _ := find(g, "exposure_mode"); f.Severity != SeverityOK {
		t.Errorf("public exposure OK expected, got %v", f.Severity)
	}
	if f, _ := find(g, "metrics_token_set"); f.Severity != SeverityOK {
		t.Errorf("metrics token OK expected, got %v", f.Severity)
	}

	// Unhealthy: no master key, local exposure, no metrics token.
	bad := NewPosture(st, nil, PostureConfig{Exposure: "local", MasterKeyPresent: false, MetricsTokenSet: false})
	b := bad.Check(context.Background())
	if f, _ := find(b, "master_key_present"); f.Severity != SeverityError {
		t.Errorf("missing master key should be error, got %v", f.Severity)
	}
	if f, _ := find(b, "exposure_mode"); f.Severity != SeverityWarn {
		t.Errorf("local exposure should warn, got %v", f.Severity)
	}
	if f, _ := find(b, "metrics_token_set"); f.Severity != SeverityWarn {
		t.Errorf("missing metrics token should warn, got %v", f.Severity)
	}
}

func TestPosture_HostPortPublish(t *testing.T) {
	port := 8080
	st := &fakePostureStore{
		apps: []store.App{{ID: "a1", Slug: "blog"}},
		services: map[string][]store.Service{
			"a1": {{ServiceName: "web", ExposedPort: &port}},
		},
	}
	p := NewPosture(st, nil, PostureConfig{Exposure: "public", MasterKeyPresent: true})
	f, ok := find(p.Check(context.Background()), "host_port_publish")
	if !ok {
		t.Fatal("expected host_port_publish finding")
	}
	if f.Severity != SeverityWarn || f.App != "blog" || f.Service != "web" {
		t.Errorf("host port finding wrong: %+v", f)
	}
}

func TestPosture_NoHostPortIsOK(t *testing.T) {
	st := &fakePostureStore{
		apps: []store.App{{ID: "a1", Slug: "blog"}},
		services: map[string][]store.Service{
			"a1": {{ServiceName: "web"}}, // no exposed port
		},
	}
	p := NewPosture(st, nil, PostureConfig{Exposure: "public", MasterKeyPresent: true})
	f, ok := find(p.Check(context.Background()), "host_port_publish")
	if !ok || f.Severity != SeverityOK {
		t.Errorf("no host port should be OK, got %+v (ok=%v)", f, ok)
	}
}

// fakePostureHost stubs the host firewall/fail2ban reader.
type fakePostureHost struct {
	fw  FirewallState
	f2b Fail2banState
}

func (f *fakePostureHost) Firewall(context.Context) FirewallState { return f.fw }
func (f *fakePostureHost) Fail2ban(context.Context) Fail2banState { return f.f2b }

func TestPosture_FirewallAbsentIsError(t *testing.T) {
	st := &fakePostureStore{}
	// Agent reporting (Source set) but nothing found → genuine absence.
	host := &fakePostureHost{
		fw:  FirewallState{Detected: false, Source: "agent"},
		f2b: Fail2banState{Detected: false, Source: "agent"},
	}
	p := NewPosture(st, host, PostureConfig{HostAgentEnabled: true, ExpectFirewall: true, ExpectFail2ban: true})
	findings := p.Check(context.Background())
	if f, _ := find(findings, "firewall"); f.Severity != SeverityError {
		t.Errorf("absent firewall should be error, got %v", f.Severity)
	}
	if f, _ := find(findings, "fail2ban"); f.Severity != SeverityWarn {
		t.Errorf("absent fail2ban should warn, got %v", f.Severity)
	}
}

func TestPosture_FirewallActiveIsOK(t *testing.T) {
	st := &fakePostureStore{}
	host := &fakePostureHost{
		fw:  FirewallState{Detected: true, Active: true, Backend: "ufw", Source: "agent"},
		f2b: Fail2banState{Detected: true, Jails: []Fail2banJail{{Name: "sshd"}}, Source: "agent"},
	}
	p := NewPosture(st, host, PostureConfig{HostAgentEnabled: true, ExpectFirewall: true, ExpectFail2ban: true})
	findings := p.Check(context.Background())
	if f, _ := find(findings, "firewall"); f.Severity != SeverityOK {
		t.Errorf("active firewall should be OK, got %v", f.Severity)
	}
	if f, _ := find(findings, "fail2ban"); f.Severity != SeverityOK {
		t.Errorf("running fail2ban with jails should be OK, got %v", f.Severity)
	}
}

func TestPosture_OptOutSuppressesWarning(t *testing.T) {
	st := &fakePostureStore{}
	host := &fakePostureHost{
		fw:  FirewallState{Detected: false, Source: "agent"},
		f2b: Fail2banState{Detected: false, Source: "agent"},
	}
	// Operator opted out of both checks — absence must not warn.
	p := NewPosture(st, host, PostureConfig{HostAgentEnabled: true, ExpectFirewall: false, ExpectFail2ban: false})
	findings := p.Check(context.Background())
	if f, _ := find(findings, "firewall"); f.Severity != SeverityOK {
		t.Errorf("opted-out firewall should be OK, got %v", f.Severity)
	}
	if f, _ := find(findings, "fail2ban"); f.Severity != SeverityOK {
		t.Errorf("opted-out fail2ban should be OK, got %v", f.Severity)
	}
}

func TestPosture_AgentOffIsNeutral(t *testing.T) {
	st := &fakePostureStore{}
	// No host data at all (Source unset) and agent off → neutral "monitoring off",
	// never a false "no firewall" alarm.
	host := &fakePostureHost{fw: FirewallState{Detected: false}, f2b: Fail2banState{Detected: false}}
	p := NewPosture(st, host, PostureConfig{HostAgentEnabled: false, ExpectFirewall: true, ExpectFail2ban: true})
	findings := p.Check(context.Background())
	if f, _ := find(findings, "firewall"); f.Severity != SeverityOK {
		t.Errorf("firewall with agent off should be neutral OK, got %v", f.Severity)
	}
	if f, _ := find(findings, "fail2ban"); f.Severity != SeverityOK {
		t.Errorf("fail2ban with agent off should be neutral OK, got %v", f.Severity)
	}
}

func TestPosture_AgentEnabledButSilentWarns(t *testing.T) {
	st := &fakePostureStore{}
	// Agent enabled but no data yet (Source unset) → warn it isn't reporting.
	host := &fakePostureHost{fw: FirewallState{Detected: false}, f2b: Fail2banState{Detected: false}}
	p := NewPosture(st, host, PostureConfig{HostAgentEnabled: true, ExpectFirewall: true, ExpectFail2ban: true})
	findings := p.Check(context.Background())
	if f, _ := find(findings, "firewall"); f.Severity != SeverityWarn {
		t.Errorf("enabled-but-silent firewall should warn, got %v", f.Severity)
	}
}

func TestPosture_StoreErrorDegrades(t *testing.T) {
	st := &fakePostureStore{appsErr: context.DeadlineExceeded}
	p := NewPosture(st, nil, PostureConfig{Exposure: "public", MasterKeyPresent: true})
	findings := p.Check(context.Background())
	// Config rules still report, plus an app_scan warning.
	if _, ok := find(findings, "master_key_present"); !ok {
		t.Error("config rules should still report on store error")
	}
	if f, ok := find(findings, "app_scan"); !ok || f.Severity != SeverityWarn {
		t.Errorf("expected app_scan warning on store error, got %+v (ok=%v)", f, ok)
	}
}
