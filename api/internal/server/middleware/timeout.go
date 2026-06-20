package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Timeout applies a per-request deadline to ordinary HTTP requests but leaves
// WebSocket upgrades untouched. chi's stock Timeout middleware wraps r.Context()
// with a fixed deadline; when mounted at the router root it bleeds into the
// long-lived WS handlers (live logs, stats, deployment streams, host vitals, and
// the interactive exec shell), which all derive from r.Context() and would be
// force-cancelled mid-stream once the budget elapses. A WS connection's lifetime
// is governed by the hub / client disconnect, not a wall-clock deadline, so we
// skip the deadline for upgrade requests and apply it to everything else.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isWebSocketUpgrade(r) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isWebSocketUpgrade reports whether r is a WebSocket handshake: an Upgrade
// header naming "websocket" plus an "upgrade" token in Connection (which may be
// a comma-separated list, e.g. "keep-alive, Upgrade").
func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, tok := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
			return true
		}
	}
	return false
}
