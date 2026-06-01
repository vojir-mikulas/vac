package middleware

import (
	"crypto/subtle"
	"net/http"
)

// MetricsToken guards the Prometheus `/metrics` exposition and the `/debug/*`
// runtime-introspection endpoints with a bearer token. These surfaces leak
// instance topology and runtime internals, so they are default-closed: when the
// configured token is empty the wrapped handler is never reached and the request
// gets a 404 (the feature is simply off, indistinguishable from "no such
// route"). A present-but-wrong token gets a 401.
//
// The token is compared in constant time. It is read from the Authorization
// header only (Bearer scheme) — never a query parameter — so it can't leak into
// the chi request logger.
func MetricsToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				http.NotFound(w, r)
				return
			}
			got, _ := bearerToken(r)
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="vac-metrics"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
