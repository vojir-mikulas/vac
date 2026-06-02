package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/security"
)

type fakePosture struct{ findings []security.PostureFinding }

func (f fakePosture) Check(context.Context) []security.PostureFinding { return f.findings }

type fakeTraffic struct{ snap security.Snapshot }

func (f fakeTraffic) Snapshot(int) security.Snapshot { return f.snap }

type fakeSecHost struct {
	f2b security.Fail2banState
	fw  security.FirewallState
}

func (f fakeSecHost) Fail2ban(context.Context) security.Fail2banState { return f.f2b }
func (f fakeSecHost) Firewall(context.Context) security.FirewallState { return f.fw }

func TestSecurityPostureHandler(t *testing.T) {
	h := SecurityPostureHandler(fakePosture{findings: []security.PostureFinding{
		{Severity: security.SeverityOK, Code: "master_key_present"},
	}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/security/posture", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got []security.PostureFinding
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Code != "master_key_present" {
		t.Errorf("findings = %+v", got)
	}
}

func TestSecurityTrafficHandler(t *testing.T) {
	h := SecurityTrafficHandler(fakeTraffic{snap: security.Snapshot{TrackedIPs: 3}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/security/traffic", nil))
	var got security.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TrackedIPs != 3 {
		t.Errorf("tracked = %d, want 3", got.TrackedIPs)
	}
}

func TestSecurityTrafficHandler_NilMonitor(t *testing.T) {
	// When the monitor is disabled the handler still returns a valid empty
	// snapshot rather than panicking or erroring.
	h := SecurityTrafficHandler(nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/security/traffic", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got security.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TopTalkers == nil || got.RecentAnomalies == nil {
		t.Errorf("nil-monitor snapshot should have non-nil slices: %+v", got)
	}
}

func TestSecurityHostHandlers(t *testing.T) {
	host := fakeSecHost{
		f2b: security.Fail2banState{Detected: true, Jails: []security.Fail2banJail{{Name: "sshd", CurrentlyBanned: 1}}},
		fw:  security.FirewallState{Detected: true, Backend: "ufw", Active: true},
	}
	rr := httptest.NewRecorder()
	SecurityFail2banHandler(host).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/security/fail2ban", nil))
	var f2b security.Fail2banState
	if err := json.Unmarshal(rr.Body.Bytes(), &f2b); err != nil || !f2b.Detected {
		t.Fatalf("fail2ban response wrong: %+v err=%v", f2b, err)
	}

	rr = httptest.NewRecorder()
	SecurityFirewallHandler(host).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/security/firewall", nil))
	var fw security.FirewallState
	if err := json.Unmarshal(rr.Body.Bytes(), &fw); err != nil || fw.Backend != "ufw" {
		t.Fatalf("firewall response wrong: %+v err=%v", fw, err)
	}
}
