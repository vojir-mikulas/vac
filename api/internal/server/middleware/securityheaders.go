package middleware

import "net/http"

// SecurityHeaders sets a baseline set of defensive response headers on every
// reply: a strict CSP for the SPA, X-Frame-Options to block framing,
// X-Content-Type-Options to disable MIME sniffing, a Referrer-Policy that
// keeps URLs off third-party referers, and HSTS so browsers refuse plaintext
// once they've seen us over TLS.
//
// CSP is intentionally tight — the SPA is self-contained, talks only to its
// own origin (and `wss:` for the live streams), and inlines no scripts.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self' data:; "+
				"connect-src 'self' ws: wss:; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
