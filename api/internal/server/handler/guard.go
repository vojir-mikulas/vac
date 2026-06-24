package handler

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/guard"
)

// Guard path scheme. The verify endpoint is hit only as Caddy's forward_auth
// subrequest (rewritten to guardVerifyPath); the start portal is hit by a
// top-level browser navigation on the control domain; the callback path is
// virtual — it only ever appears in the forwarded URI, intercepted by verify,
// and is never a real route.
const (
	guardVerifyPath   = "/__vac_guard"
	guardStartPath    = "/__vac_guard/start"
	guardCallbackPath = "/__vac_guard/callback"
)

// GuardHostChecker reports whether a hostname is fronted by the VAC login gate.
// *proxy.Manager satisfies it; the start portal uses it to refuse an open
// redirect to any host VAC doesn't actually guard.
type GuardHostChecker interface {
	IsGuardedHost(ctx context.Context, host string) (bool, error)
}

// GuardVerify is the forward_auth target for guarded routes (internal/guard).
// Caddy rewrites every guarded request to guardVerifyPath and proxies here,
// carrying the original host/URI in X-Vac-Guard-Host / X-Vac-Guard-Uri and the
// shared secret in X-Caddy-Ask-Token (lifted from ?t= by scrubCaddyAskToken).
// It answers one of three ways, each of which Caddy turns into the right client
// behaviour:
//
//   - 204: a valid guard cookie for this host — Caddy's handle_response falls
//     through to the real upstream, with the resolved user on X-Vac-User.
//   - 302 + Set-Cookie (callback leg): the visitor returned from the portal with
//     a fresh exchange token; trade it for a host cookie. Caddy copies this
//     non-2xx response — redirect and cookie — back to the browser.
//   - 302 to the portal: anonymous; bounce to the control-plane login, carrying
//     the original target so the visitor lands back where they started.
//
// Stateless: every decision is an HMAC check, no DB round-trip on the hot path.
func GuardVerify(signer *guard.Signer, controlDomain, askToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fail closed when the guard can't operate (no master key, or no control
		// domain to host the portal). Caddy copies this non-2xx to the client.
		if signer == nil || controlDomain == "" {
			http.Error(w, "vac guard is not configured", http.StatusServiceUnavailable)
			return
		}
		// Only Caddy knows the shared secret; reject anything else poking the
		// endpoint directly (defence in depth — every state change is HMAC-gated
		// regardless).
		if askToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Caddy-Ask-Token")), []byte(askToken)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		host := hostOnly(r.Header.Get("X-Vac-Guard-Host"))
		if host == "" {
			host = hostOnly(r.Host)
		}
		uri := r.Header.Get("X-Vac-Guard-Uri")

		// Callback leg: arriving back from the portal with an exchange token. Trade
		// it for a host-scoped guard cookie and send the visitor on to their target.
		if query, ok := guardCallbackQuery(uri); ok {
			q, _ := url.ParseQuery(query)
			if user, valid := signer.Verify(guard.KindExchange, host, q.Get("token")); valid {
				setGuardCookie(w, r, signer.Mint(guard.KindSession, host, user, guard.CookieTTL))
				http.Redirect(w, r, guardCleanRedirect(host, q.Get("rd")), http.StatusFound)
				return
			}
			// Stale or forged exchange token — restart the dance from a clean slate.
			http.Redirect(w, r, guardStartURL(controlDomain, host, "/"), http.StatusFound)
			return
		}

		// Already authenticated for this host?
		if c, err := r.Cookie(auth.GuardCookie); err == nil && c.Value != "" {
			if user, ok := signer.Verify(guard.KindSession, host, c.Value); ok {
				if user != "" {
					w.Header().Set("X-Vac-User", user)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		// Anonymous → bounce to the control-plane portal, remembering the target.
		http.Redirect(w, r, guardStartURL(controlDomain, host, uri), http.StatusFound)
	}
}

// GuardStart is the control-plane login portal for the guard. It runs on the
// control domain (reached by a top-level browser navigation from GuardVerify's
// bounce). It validates that rd points at a genuinely guarded host, then either
// sends the visitor through the normal dashboard login (when they have no
// session) or mints a short-lived exchange token and hands off to the guarded
// host's callback. The control-plane session never leaves this domain — only the
// host-bound exchange token crosses to the app.
func GuardStart(sm *auth.SessionManager, signer *guard.Signer, guardChk GuardHostChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || guardChk == nil {
			http.Error(w, "vac guard is not configured", http.StatusServiceUnavailable)
			return
		}
		rd := r.URL.Query().Get("rd")
		host, ok := guardRedirectHost(rd)
		if !ok {
			http.Error(w, "invalid redirect target", http.StatusBadRequest)
			return
		}
		// Open-redirect / SSRF guard: only ever bounce to a host VAC actually
		// fronts with the login gate.
		guarded, err := guardChk.IsGuardedHost(r.Context(), host)
		if err != nil {
			http.Error(w, "could not resolve host", http.StatusInternalServerError)
			return
		}
		if !guarded {
			http.Error(w, "not a guarded host", http.StatusForbidden)
			return
		}

		user := guardSessionUser(r, sm)
		if user == "" {
			// No dashboard session yet: through the normal login, returning here
			// afterward. A relative Location keeps it on the control domain (this
			// request's own origin), and the SPA honours ?next on success.
			next := guardStartPath + "?rd=" + url.QueryEscape(rd)
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
			return
		}

		tok := signer.Mint(guard.KindExchange, host, user, guard.ExchangeTTL)
		cb := "https://" + host + guardCallbackPath +
			"?token=" + url.QueryEscape(tok) + "&rd=" + url.QueryEscape(rd)
		http.Redirect(w, r, cb, http.StatusFound)
	}
}

// setGuardCookie issues the host-scoped guard cookie. No Domain attribute, so it
// is host-only — scoped to this one app, never shared with the control plane or
// other apps. SameSite=Lax (not Strict) because the visitor first arrives at the
// guarded host via a cross-site top-level redirect from the portal, which a
// Strict cookie would not accompany.
func setGuardCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.GuardCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureForRequest(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(guard.CookieTTL.Seconds()),
	})
}

// guardSessionUser resolves the control-plane session cookie to a username, or
// "" when absent/invalid. The guard endpoints sit outside the /api auth
// middleware, so they read the session directly.
func guardSessionUser(r *http.Request, sm *auth.SessionManager) string {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	_, user, err := sm.Lookup(r.Context(), c.Value)
	if err != nil {
		return ""
	}
	return user.Username
}

// guardCallbackQuery returns the raw query string when uri is the guard callback
// path, else ok=false. uri is the original (pre-rewrite) request URI carried in
// X-Vac-Guard-Uri.
func guardCallbackQuery(uri string) (query string, ok bool) {
	path, query, _ := strings.Cut(uri, "?")
	if path != guardCallbackPath {
		return "", false
	}
	return query, true
}

// guardRedirectHost extracts the (lowercased, port-stripped) host from an rd
// target, requiring an absolute https URL with a host. Anything else is rejected
// so the portal can't be coerced into an open redirect.
func guardRedirectHost(rd string) (string, bool) {
	u, err := url.Parse(rd)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	return hostOnly(u.Host), true
}

// guardCleanRedirect rebuilds a safe absolute redirect to host from a client-
// supplied rd: it keeps only the path+query and requires the same host, so the
// cookie-setting response can't be redirected to a different origin. Falls back
// to the host root on any mismatch.
func guardCleanRedirect(host, rd string) string {
	u, err := url.Parse(rd)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(hostOnly(u.Host), host) {
		return "https://" + host + "/"
	}
	out := "https://" + host + guardSafePath(u.EscapedPath())
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out
}

// guardStartURL builds the absolute portal URL on the control domain, carrying
// the original target as rd (constrained to an absolute path on host so a
// client-supplied URI can't inject a scheme/host).
func guardStartURL(controlDomain, host, uri string) string {
	rd := "https://" + host + guardSafePath(uri)
	return "https://" + controlDomain + guardStartPath + "?rd=" + url.QueryEscape(rd)
}

// guardSafePath constrains a client-controlled URI to an absolute path (no
// scheme/host spoofing, no protocol-relative "//evil"), mirroring wakeTarget.
func guardSafePath(uri string) string {
	if i := strings.IndexAny(uri, " \t\r\n"); i >= 0 {
		uri = uri[:i]
	}
	if uri == "" || uri[0] != '/' || strings.HasPrefix(uri, "//") {
		return "/"
	}
	return uri
}
