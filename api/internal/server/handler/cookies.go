package handler

import (
	"net/http"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

// secureForRequest reports whether the cookies set on this response should
// carry the Secure attribute. Decided per-request: a TLS connection direct to
// vac-api, or an X-Forwarded-Proto=https header from vac-proxy in front,
// counts as secure. Plain-HTTP requests (e.g. http://<vps-ip>:9393 on
// first boot) do not — marking cookies Secure there would make browsers drop
// them and silently break login.
//
// Trusting X-Forwarded-Proto is intentional: vac-proxy is the only reverse
// proxy in the bundled topology and sets the header. Operators putting a
// second proxy in front must terminate TLS upstream or pass the header
// through; see docs/deployment.md.
func secureForRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func setCSRFCookie(w http.ResponseWriter, r *http.Request, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:  auth.CSRFCookie,
		Value: value,
		Path:  "/",
		// Not HttpOnly — JS reads it to echo back in the CSRF header.
		HttpOnly: false,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func clearCSRFCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func setPreAuthCookie(w http.ResponseWriter, r *http.Request, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.PreAuthCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearPreAuthCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.PreAuthCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
