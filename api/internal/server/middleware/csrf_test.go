package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCSRF_SafeMethodsPassThrough(t *testing.T) {
	h := CSRF(okHandler())
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(m, func(t *testing.T) {
			req := httptest.NewRequest(m, "/anywhere", nil)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "sess"})
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rr.Code)
			}
		})
	}
}

func TestCSRF_AnonymousMutatingPassesThrough(t *testing.T) {
	// POST /api/auth/login has no session cookie yet.
	h := CSRF(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestCSRF_BearerAuthBypasses(t *testing.T) {
	h := CSRF(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/apps", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "sess"})
	req.Header.Set("Authorization", "Bearer vac_some-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestCSRF_SessionWithoutHeader_403(t *testing.T) {
	h := CSRF(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/apps", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "sess"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: "csrf-token"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestCSRF_HeaderMismatch_403(t *testing.T) {
	h := CSRF(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/apps", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "sess"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: "right"})
	req.Header.Set(auth.CSRFHeader, "wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestCSRF_HeaderMatch_Passes(t *testing.T) {
	h := CSRF(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/apps", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "sess"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: "matching-token"})
	req.Header.Set(auth.CSRFHeader, "matching-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}
