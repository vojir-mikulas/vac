package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/config"
)

func TestInstanceInfoReportsBuildMetadata(t *testing.T) {
	t.Parallel()
	cfg := config.Config{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-05-31T00:00:00Z"}
	rr := httptest.NewRecorder()
	InstanceInfo(cfg)(rr, httptest.NewRequest(http.MethodGet, "/api/instance/info", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["version"] != "1.2.3" || body["built_at"] != "2026-05-31T00:00:00Z" {
		t.Errorf("body = %+v; want version/built_at echoed", body)
	}
	if body["channel"] != "stable" {
		t.Errorf("channel = %q; want stable", body["channel"])
	}
	// Track D gate defaults off.
	if body["managed_services"] != false {
		t.Errorf("managed_services = %v; want false by default", body["managed_services"])
	}
}

func TestDNSCheckRejectsInvalidHost(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DNSCheck("203.0.113.10")(rr, httptest.NewRequest(http.MethodGet, "/api/instance/dns-check?host=not-a-domain", nil))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 for a single-label host", rr.Code)
	}
}

func TestDNSCheckUnresolvedHostReportsNotPointing(t *testing.T) {
	t.Parallel()
	// .invalid is reserved (RFC 6761) and never resolves, so this exercises the
	// "not pointed yet" path without depending on live DNS.
	rr := httptest.NewRecorder()
	DNSCheck("203.0.113.10")(rr, httptest.NewRequest(http.MethodGet, "/api/instance/dns-check?host=nope.invalid", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (unresolved is a normal state)", rr.Code)
	}
	var body struct {
		PointsHere bool     `json:"points_here"`
		Resolved   []string `json:"resolved"`
		IP         string   `json:"ip"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PointsHere {
		t.Errorf("points_here = true; want false for an unresolved host")
	}
	if body.IP != "203.0.113.10" {
		t.Errorf("ip = %q; want the VPS IP echoed", body.IP)
	}
}

func TestResetInstanceRejectsWrongConfirmation(t *testing.T) {
	t.Parallel()
	// Wrong confirmation is rejected before any store/docker interaction, so nil
	// dependencies are safe here.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/instance/reset", strings.NewReader(`{"confirm":"nope"}`))
	ResetInstance(nil, nil, nil)(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 for a mismatched confirmation", rr.Code)
	}
}
