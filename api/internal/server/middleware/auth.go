// Package middleware holds HTTP middleware used by the chi router.
package middleware

import (
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
)

// Auth reads the session cookie and, if valid, injects the user + session
// into the request context. It never rejects on its own — that is what
// RequireSession is for. This lets us mount one middleware stack and then
// gate individual subroutes on whether they need auth.
func Auth(sm *auth.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(auth.SessionCookie)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			sess, user, err := sm.Lookup(r.Context(), c.Value)
			if err != nil {
				// Unknown / expired tokens are treated as anonymous; the
				// handler decides whether to demand auth.
				next.ServeHTTP(w, r)
				return
			}
			ctx := auth.WithUser(r.Context(), &user)
			ctx = auth.WithSession(ctx, &sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireSession blocks unauthenticated requests with 401. Use it on route
// groups that require a logged-in user.
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.User(r.Context()) == nil {
			handler.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
