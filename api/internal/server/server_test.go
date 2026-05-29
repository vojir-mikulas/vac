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
	srv := New(t.Context(), config.Default(), nil, nil, nil)
	return srv.Handler
}

func TestHealth(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	newTestServer(t).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q", rr.Body.String())
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
