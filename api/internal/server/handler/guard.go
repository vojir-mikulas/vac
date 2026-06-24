package handler

import (
	"context"
	"crypto/subtle"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/auth"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
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
	guardRedeemPath   = "/__vac_guard/redeem"
)

// guardGuestUser is the X-Vac-User label for a visitor who entered the shared
// access code (vs. an operator, who gets their username). Lets a guarded app
// tell guests from operators.
const guardGuestUser = "guest"

// GuardHostResolver resolves a hostname to the (app, service) fronted by the VAC
// login gate at that host. *proxy.Manager satisfies it; the portal uses it to
// refuse an open redirect and to find the service whose shared access code
// applies.
type GuardHostResolver interface {
	GuardedServiceForHost(ctx context.Context, host string) (appID, service string, ok bool, err error)
}

// GuestAccessReader reads a service's sealed shared access code. *store.Store
// satisfies it; kept narrow so the portal handlers stay unit-testable.
type GuestAccessReader interface {
	GetServiceGuestAccessCode(ctx context.Context, appID, service string) ([]byte, error)
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
// bounce). It validates that rd points at a genuinely guarded host, then:
//
//   - operator with a live dashboard session → mint an exchange token and hand
//     off to the guarded host's callback (the control-plane session never leaves
//     this domain; only the host-bound token crosses);
//   - anonymous, app has a shared access code → render the access-code page so a
//     friend can enter the code (GuardRedeem completes the handoff);
//   - anonymous, no access code → through the normal dashboard login, returning
//     here afterward.
func GuardStart(sm *auth.SessionManager, signer *guard.Signer, resolver GuardHostResolver, codes GuestAccessReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || resolver == nil {
			http.Error(w, "vac guard is not configured", http.StatusServiceUnavailable)
			return
		}
		rd := r.URL.Query().Get("rd")
		host, ok := guardRedirectHost(rd)
		if !ok {
			http.Error(w, "invalid redirect target", http.StatusBadRequest)
			return
		}
		// Open-redirect / SSRF guard: only ever act on a host VAC actually fronts
		// with the login gate.
		appID, service, guarded, err := resolver.GuardedServiceForHost(r.Context(), host)
		if err != nil {
			http.Error(w, "could not resolve host", http.StatusInternalServerError)
			return
		}
		if !guarded {
			http.Error(w, "not a guarded host", http.StatusForbidden)
			return
		}

		// Operator already signed in to the dashboard → straight handoff.
		if user := guardSessionUser(r, sm); user != "" {
			http.Redirect(w, r, guardCallbackURL(signer, host, user, rd), http.StatusFound)
			return
		}

		// Anonymous: offer the shared access code when this service has one set,
		// otherwise fall back to the operator login.
		if enc, _ := codes.GetServiceGuestAccessCode(r.Context(), appID, service); len(enc) > 0 {
			writeGuardAccessPage(w, http.StatusOK, rd, "")
			return
		}
		// A relative Location keeps it on the control domain (this request's own
		// origin); the SPA honours ?next on success.
		next := guardStartPath + "?rd=" + url.QueryEscape(rd)
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
	}
}

// GuardRedeem handles the shared-access-code form POST from the access page. On a
// correct code it mints a guest exchange token and hands off to the guarded
// host's callback (identical to the operator path, but labelled "guest"); on a
// wrong code it re-renders the page with an error. Must be rate-limited at the
// router — it is the brute-force surface for the code.
func GuardRedeem(signer *guard.Signer, resolver GuardHostResolver, codes GuestAccessReader, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if signer == nil || resolver == nil || box == nil {
			http.Error(w, "vac guard is not configured", http.StatusServiceUnavailable)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		rd := r.PostForm.Get("rd")
		host, ok := guardRedirectHost(rd)
		if !ok {
			http.Error(w, "invalid redirect target", http.StatusBadRequest)
			return
		}
		appID, service, guarded, err := resolver.GuardedServiceForHost(r.Context(), host)
		if err != nil {
			http.Error(w, "could not resolve host", http.StatusInternalServerError)
			return
		}
		if !guarded {
			http.Error(w, "not a guarded host", http.StatusForbidden)
			return
		}
		enc, err := codes.GetServiceGuestAccessCode(r.Context(), appID, service)
		if err != nil || len(enc) == 0 {
			http.Error(w, "shared access is not enabled", http.StatusForbidden)
			return
		}
		want, err := box.Open(enc)
		if err != nil {
			http.Error(w, "could not read access code", http.StatusInternalServerError)
			return
		}
		got := strings.TrimSpace(r.PostForm.Get("code"))
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeGuardAccessPage(w, http.StatusUnauthorized, rd, "Incorrect access code.")
			return
		}
		http.Redirect(w, r, guardCallbackURL(signer, host, guardGuestUser, rd), http.StatusFound)
	}
}

// guardCallbackURL builds the absolute hand-off URL to the guarded host's
// callback, carrying a freshly-minted exchange token bound to (host, user).
func guardCallbackURL(signer *guard.Signer, host, user, rd string) string {
	tok := signer.Mint(guard.KindExchange, host, user, guard.ExchangeTTL)
	return "https://" + host + guardCallbackPath +
		"?token=" + url.QueryEscape(tok) + "&rd=" + url.QueryEscape(rd)
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

// writeGuardAccessPage renders the self-contained shared-access-code page served
// to anonymous visitors of a guarded service that has a code set. It deliberately
// does NOT load the dashboard SPA — a guest never touches the control plane — but
// it mirrors the dashboard's look: the VAC colour tokens (kept in sync with
// ui/src/index.css), the brand logo (served same-origin on the control domain),
// and light/dark via prefers-color-scheme so it follows the visitor's OS with no
// JavaScript. rd is reflected into a hidden field and the operator link, so both
// are HTML-escaped; errMsg is one of a few fixed strings.
func writeGuardAccessPage(w http.ResponseWriter, status int, rd, errMsg string) {
	rdAttr := html.EscapeString(rd)
	// Operator escape hatch: through the normal login, returning to the portal.
	operatorHref := html.EscapeString("/login?next=" + url.QueryEscape(guardStartPath+"?rd="+url.QueryEscape(rd)))

	errBlock := ""
	if errMsg != "" {
		errBlock = `<p class="err">` + html.EscapeString(errMsg) + `</p>`
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Access required · VAC</title>
<style>
  /* VAC design tokens (mirrors ui/src/index.css). Light is the default; dark is
     applied on OS preference — both supported, no JS. */
  :root{
    --bg:oklch(0.978 0.003 60); --card:oklch(0.995 0.002 60);
    --fg:oklch(0.18 0.005 60); --muted:oklch(0.56 0.008 60);
    --border:oklch(0.91 0.004 60);
    --brand:#0057b8; --brand-fg:#fff; --radius:0.625rem;
    --err-fg:oklch(0.42 0.16 25); --err-bg:oklch(0.97 0.03 25); --err-border:oklch(0.86 0.08 25);
    --dot:color-mix(in oklch, var(--fg), transparent 93%);
  }
  @media (prefers-color-scheme: dark){
    :root{
      --bg:oklch(0.205 0.006 60); --card:oklch(0.165 0.005 60);
      --fg:oklch(0.96 0.004 60); --muted:oklch(0.62 0.01 60);
      --border:oklch(0.3 0.008 60);
      --err-fg:oklch(0.82 0.14 25); --err-bg:oklch(0.28 0.08 25); --err-border:oklch(0.38 0.12 25);
    }
  }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;display:flex;align-items:center;justify-content:center;padding:3rem 1rem;
       position:relative;background:var(--bg);color:var(--fg);
       font-family:'Geist',ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif;
       -webkit-font-smoothing:antialiased;-moz-osx-font-smoothing:grayscale}
  /* Faint static dot lattice — echoes the dashboard's auth backdrop. */
  body::before{content:"";position:fixed;inset:0;pointer-events:none;
       background-image:radial-gradient(var(--dot) 1.2px,transparent 1.6px);background-size:24px 24px}
  .shell{position:relative;width:min(23rem,92vw)}
  .head{display:flex;flex-direction:column;align-items:center;gap:.75rem;text-align:center;margin-bottom:2rem}
  .logo{width:2.5rem;height:2.5rem;border-radius:.6rem}
  h1{font-size:1.125rem;font-weight:600;letter-spacing:-.01em;margin:0}
  .sub{margin:.3rem 0 0;color:var(--muted);font-size:.875rem}
  .card{background:var(--card);border:1px solid var(--border);border-radius:var(--radius);
        padding:1.5rem;box-shadow:0 1px 2px rgba(0,0,0,.04),0 10px 34px rgba(0,0,0,.07)}
  label{display:block;font-size:.8rem;font-weight:500;margin:0 0 .4rem}
  input{width:100%;padding:.55rem .7rem;background:transparent;color:var(--fg);font-family:inherit;
        border:1px solid var(--border);border-radius:calc(var(--radius) - 2px);font-size:.95rem}
  input::placeholder{color:var(--muted)}
  input:focus{outline:none;border-color:var(--brand);
        box-shadow:0 0 0 3px color-mix(in oklch,var(--brand),transparent 70%)}
  button{width:100%;margin-top:1rem;padding:.6rem;border:0;border-radius:calc(var(--radius) - 2px);
        background:var(--brand);color:var(--brand-fg);font-size:.95rem;font-weight:600;
        font-family:inherit;cursor:pointer}
  button:hover{background:color-mix(in oklch,var(--brand),black 12%)}
  .err{margin:0 0 1rem;padding:.55rem .7rem;border-radius:calc(var(--radius) - 2px);
        background:var(--err-bg);border:1px solid var(--err-border);color:var(--err-fg);font-size:.85rem}
  .alt{margin:1.25rem 0 0;text-align:center;font-size:.8rem}
  .alt a{color:var(--muted);text-decoration:none}
  .alt a:hover{color:var(--fg);text-decoration:underline}
</style>
</head>
<body>
  <main class="shell">
    <div class="head">
      <img class="logo" src="/vac-logo.svg" alt="VAC">
      <div>
        <h1>Enter access code</h1>
        <p class="sub">This site is private. Enter the access code you were given to continue.</p>
      </div>
    </div>
    <div class="card">
      ` + errBlock + `
      <form method="post" action="` + guardRedeemPath + `">
        <input type="hidden" name="rd" value="` + rdAttr + `">
        <label for="code">Access code</label>
        <input id="code" type="password" name="code" autocomplete="off" autofocus>
        <button type="submit">Continue</button>
      </form>
      <p class="alt"><a href="` + operatorHref + `">I&#39;m the operator — sign in</a></p>
    </div>
  </main>
</body>
</html>`))
}
