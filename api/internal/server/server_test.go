package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/config"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	srv, err := New(t.Context(), config.Default(), nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv.Handler
}

func TestHealth(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	newTestServer(t).ServeHTTP(rr, req)

	// 200 when docker is reachable, 503 when it isn't — either is a valid
	// signal to a load balancer; we only assert the response shape is JSON
	// and includes the three status fields.
	if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 200 or 503", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	body := rr.Body.String()
	for _, field := range []string{`"status"`, `"database"`, `"docker"`} {
		if !strings.Contains(body, field) {
			t.Errorf("body missing %s field: %s", field, body)
		}
	}
}

func TestUnknownAPIPath404sAsJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	newTestServer(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q (api routes should never fall through to the UI handler)", ct)
	}
}

func TestUnknownNonAPIPathServesUI(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/spa/route", nil)
	newTestServer(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", rr.Code)
	}
}
