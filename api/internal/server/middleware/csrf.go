package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
)

// CSRF enforces the double-submit cookie pattern on session-authenticated
// mutating requests. The client must echo the value of the CSRF cookie back
// in the CSRFHeader. Safe methods, Bearer-authenticated requests, and
// fully anonymous requests are pass-through.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// Bearer-token requests (API tokens, added in M9) carry their own
		// proof of origin and are not vulnerable to CSRF.
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(auth.SessionCookie)
		if err != nil || cookie.Value == "" {
			// Anonymous mutating requests (e.g. POST /api/auth/login) are not
			// session-authenticated and have no CSRF surface.
			next.ServeHTTP(w, r)
			return
		}
		csrfCookie, err := r.Cookie(auth.CSRFCookie)
		csrfHeader := r.Header.Get(auth.CSRFHeader)
		if err != nil || csrfCookie.Value == "" || csrfHeader == "" {
			handler.WriteErrorCode(w, http.StatusForbidden, handler.CodeCSRFMismatch, "missing csrf token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(csrfHeader)) != 1 {
			handler.WriteErrorCode(w, http.StatusForbidden, handler.CodeCSRFMismatch, "csrf token mismatch")
			return
		}
		next.ServeHTTP(w, r)
	})
}
