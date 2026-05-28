// Package middleware holds HTTP middleware used by the chi router.
package middleware

import (
	"net/http"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/server/handler"
)

// Auth resolves either the session cookie or an `Authorization: Bearer vac_…`
// API token and, on success, injects the user (and for cookie auth, also the
// session) into the request context. It never rejects on its own — that is
// what RequireSession is for. This lets us mount one middleware stack and
// then gate individual subroutes on whether they need auth.
//
// Bearer auth takes precedence over the cookie: a request that presents both
// (unusual but valid for CLI tools that ship browser cookies) is treated as
// the token's user, not the cookie's. This avoids surprising mismatch bugs
// where the two disagree.
func Auth(sm *auth.SessionManager, tm *auth.TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if raw, ok := bearerToken(r); ok {
				if _, user, err := tm.Lookup(r.Context(), raw); err == nil {
					ctx := auth.WithUser(r.Context(), &user)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Bearer present but bad: fall through as anonymous; the
				// handler / RequireSession will respond with 401.
				next.ServeHTTP(w, r)
				return
			}

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

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if raw == "" {
		return "", false
	}
	return raw, true
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
