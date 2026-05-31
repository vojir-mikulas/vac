package handler

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

func TestSecureForRequest(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(r *http.Request)
		wantSec bool
	}{
		{
			name:    "plain http",
			setup:   func(r *http.Request) {},
			wantSec: false,
		},
		{
			name: "tls connection",
			setup: func(r *http.Request) {
				r.TLS = &tls.ConnectionState{}
			},
			wantSec: true,
		},
		{
			name: "xfp https header",
			setup: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "https")
			},
			wantSec: true,
		},
		{
			name: "xfp http header",
			setup: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "http")
			},
			wantSec: false,
		},
		{
			name: "xfp empty",
			setup: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "")
			},
			wantSec: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.com/", nil)
			tc.setup(r)
			if got := secureForRequest(r); got != tc.wantSec {
				t.Errorf("secureForRequest = %v, want %v", got, tc.wantSec)
			}
		})
	}
}

func TestSetSessionCookie_RequestSchemeDrivesSecure(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(r *http.Request)
		wantSec bool
	}{
		{
			name:    "plain http drops Secure",
			setup:   func(r *http.Request) {},
			wantSec: false,
		},
		{
			name: "tls sets Secure",
			setup: func(r *http.Request) {
				r.TLS = &tls.ConnectionState{}
			},
			wantSec: true,
		},
		{
			name: "xfp=https sets Secure",
			setup: func(r *http.Request) {
				r.Header.Set("X-Forwarded-Proto", "https")
			},
			wantSec: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://example.com/api/auth/login", nil)
			tc.setup(r)
			w := httptest.NewRecorder()
			setSessionCookie(w, r, "tok", time.Hour)

			resp := w.Result()
			defer resp.Body.Close()
			cookies := resp.Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected 1 cookie, got %d", len(cookies))
			}
			c := cookies[0]
			if c.Name != auth.SessionCookie {
				t.Errorf("cookie name = %q, want %q", c.Name, auth.SessionCookie)
			}
			if c.Secure != tc.wantSec {
				t.Errorf("Secure = %v, want %v", c.Secure, tc.wantSec)
			}
			if !c.HttpOnly {
				t.Error("session cookie should be HttpOnly")
			}
			if c.SameSite != http.SameSiteStrictMode {
				t.Errorf("SameSite = %v, want Strict", c.SameSite)
			}
		})
	}
}

func TestClearCookies_HonorRequestScheme(t *testing.T) {
	r := httptest.NewRequest("POST", "http://example.com/api/auth/logout", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	clearSessionCookie(w, r)
	clearCSRFCookie(w, r)
	clearPreAuthCookie(w, r)

	for _, c := range w.Result().Cookies() {
		if !c.Secure {
			t.Errorf("%s cleared without Secure even though XFP=https", c.Name)
		}
		if c.MaxAge != -1 {
			t.Errorf("%s MaxAge = %d, want -1", c.Name, c.MaxAge)
		}
	}
}
