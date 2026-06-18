package handler

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/scaletozero"
)

// Waker triggers an app wake and blocks until it is serving (or the health
// budget expires). *scaletozero.Waker satisfies it.
type Waker interface {
	Wake(ctx context.Context, appID string) error
}

// WakeResolver maps a request host back to its app id. *proxy.Manager satisfies
// it.
type WakeResolver interface {
	AppIDForHost(ctx context.Context, host string) (string, bool, error)
}

// WakeApp backs the scale-to-zero wake route (docs/plans/scale-to-zero.md). When
// an app is suspended, Caddy swaps its routes for one that rewrites any path to
// /__vac_wake and proxies here, carrying the original host and URI in the
// X-Vac-Wake-Host / X-Vac-Wake-Uri headers. This endpoint is unauthenticated
// (like CaddyAsk) and lives outside the /api auth group; it is reachable only on
// the internal compose network and is optionally shared-secret gated.
//
// Flow: resolve host → app, trigger Wake (in-flight-deduped so concurrent
// requests cause exactly one docker start). The request that wins the race
// blocks on the health gate, then 307-redirects idempotent methods back to the
// now-live URL; everything else (the losing concurrent requests, non-idempotent
// methods whose body can't survive a redirect, and wake failures) gets an
// auto-refreshing waking page.
func WakeApp(waker Waker, resolver WakeResolver, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if waker == nil || resolver == nil {
			http.Error(w, "scale-to-zero not enabled", http.StatusNotFound)
			return
		}
		if token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Caddy-Ask-Token")), []byte(token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		host := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Vac-Wake-Host")))
		if host == "" {
			host = hostOnly(r.Host)
		}
		target := wakeTarget(host, r.Header.Get("X-Vac-Wake-Uri"))

		// The wake (docker start + health gate) must outlive the triggering
		// request: a client refresh, disconnect, or proxy idle-timeout mid-start
		// must not cancel it and leave the stack half-started. Detach from the
		// request context, keeping a bounded budget comfortably above WaitHealthy.
		wakeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
		defer cancel()

		appID, ok, err := resolver.AppIDForHost(wakeCtx, host)
		if err != nil || !ok {
			// Unknown host (nothing to wake) or a lookup error: serve the waking
			// page so the client retries rather than seeing a hard error.
			writeWakingPage(w, http.StatusBadGateway, "We couldn't find that app.")
			return
		}

		switch err := waker.Wake(wakeCtx, appID); {
		case err == nil:
			// Healthy and serving. Caddy now routes the host to the live app.
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, target, http.StatusTemporaryRedirect)
				return
			}
			// A POST/PUT body is lost on a redirect, so ask the client to retry the
			// original request against the now-live app.
			writeWakingPage(w, http.StatusServiceUnavailable, "The app is awake — retrying your request…")
		case errors.Is(err, scaletozero.ErrWaking):
			// Another request is already starting the stack; auto-refresh until it
			// finishes and the real route is back.
			writeWakingPage(w, http.StatusServiceUnavailable, "Waking the app…")
		default:
			// Start or health gate failed. Don't loop-restart — surface it and let
			// normal crashloop/health handling apply. The refresh lets the client
			// retry once the app recovers.
			writeWakingPage(w, http.StatusBadGateway, "The app didn't come up. Retrying…")
		}
	}
}

// wakeTarget builds the absolute URL to redirect back to once the app is live,
// from the original host and request URI. The URI is client-controlled, so it is
// constrained to an absolute path (no scheme/host spoofing, no protocol-relative
// "//evil") before being appended.
func wakeTarget(host, uri string) string {
	if i := strings.IndexAny(uri, " \t\r\n"); i >= 0 {
		uri = uri[:i]
	}
	if uri == "" || uri[0] != '/' || strings.HasPrefix(uri, "//") {
		uri = "/"
	}
	return "https://" + host + uri
}

// hostOnly strips any :port from a Host header.
func hostOnly(host string) string {
	host = strings.TrimSpace(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}

// writeWakingPage serves the auto-refreshing "waking…" interstitial. It carries a
// short Retry-After and a meta-refresh of the current URL, so the browser re-hits
// the same host (and thus the wake route) until the app is live.
func writeWakingPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(wakingPageHTML(message)))
}

func wakingPageHTML(message string) string {
	// Static template; the message is one of a few fixed strings (no user input),
	// so no escaping is needed.
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="2">
<title>Waking…</title>
<style>
  html,body{height:100%;margin:0}
  body{display:flex;align-items:center;justify-content:center;
       font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;
       background:#0b0d10;color:#e6e8eb}
  .card{text-align:center;max-width:28rem;padding:2rem}
  .spin{width:2.5rem;height:2.5rem;margin:0 auto 1.25rem;border-radius:50%;
        border:3px solid #2a2f36;border-top-color:#6aa3ff;animation:s 1s linear infinite}
  h1{font-size:1.1rem;font-weight:600;margin:0 0 .35rem}
  p{margin:0;color:#9aa3ad;font-size:.9rem}
  @keyframes s{to{transform:rotate(360deg)}}
</style>
</head>
<body>
  <div class="card">
    <div class="spin"></div>
    <h1>` + message + `</h1>
    <p>This page refreshes automatically. The app was idle and is starting back up.</p>
  </div>
</body>
</html>`
}
