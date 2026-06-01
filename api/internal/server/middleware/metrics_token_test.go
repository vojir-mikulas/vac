package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsToken_UnsetIsClosed(t *testing.T) {
	// Default-closed: with no configured token the endpoint must look absent.
	h := MetricsToken("")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestMetricsToken_WrongOrMissingTokenIs401(t *testing.T) {
	h := MetricsToken("s3cret")(okHandler())
	for name, hdr := range map[string]string{
		"missing":  "",
		"wrong":    "Bearer nope",
		"noscheme": "s3cret",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if hdr != "" {
				req.Header.Set("Authorization", hdr)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rr.Code)
			}
		})
	}
}

func TestMetricsToken_CorrectTokenPasses(t *testing.T) {
	h := MetricsToken("s3cret")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}
