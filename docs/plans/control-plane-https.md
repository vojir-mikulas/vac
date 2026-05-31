# Control-plane HTTPS

## Goal

Make the VAC dashboard usable on first boot and proper-HTTPS-ready as soon as the
operator points a domain at the host. Today the user installs VAC, opens
`http://<vps-ip>:3000/login`, and the login appears to fail — because the session
cookies are marked `Secure` and the browser drops them on a plain-HTTP origin.
After `vac set-domain example.com`, apps get HTTPS subdomains via Caddy's
on-demand TLS, but the dashboard itself keeps serving on `http://<ip>:3000`.

By the end of this work:

1. `http://<vps-ip>:3000` accepts logins and persists sessions — first-boot is
   not gated on having a domain.
2. `vac set-domain example.com` puts the dashboard on `https://vac.example.com`
   with a real Let's Encrypt cert, no extra steps.
3. The UI warns the operator when traffic is unencrypted and points at the fix.
4. (Deferred) Operators with no domain can still get HTTPS via a real Let's
   Encrypt certificate issued directly for the host's public IP — printed on
   first boot.

## Background

What today's code does (`/Users/mikulasvojir/Documents/Projects/vac/api/internal/config/config.go:278`):

```go
func (c Config) SecureCookies() bool {
    return c.Exposure == ExposurePublic
}
```

Every cookie call site (`api/internal/server/handler/cookies.go`,
`api/internal/auth/cookies.go`) reads this once at write time. With the default
`Exposure=public`, cookies are always `Secure`, regardless of whether the
current request actually arrived over TLS.

vac-proxy publishes 80/443 and Caddy already speaks ACME with an on-demand TLS
ask-hook back to vac-api (`api/internal/caddy/config.go` `BaseConfig`,
`/internal/caddy/ask`). The proxy.Manager rebuilds Caddy routes per app
deploy, keyed off `cfg.BaseDomain`, but never adds a route for the dashboard
itself. `vac set-domain` only stores `VAC_BASE_DOMAIN` and restarts vac-api —
which affects auto-domains, webhook URLs, and the WS Origin allowlist, nothing
else.

The infrastructure to issue a real cert for the dashboard is therefore *already
in place*; the missing pieces are (a) a Caddy route for the dashboard host, and
(b) cookies that don't break the user before they get there.

---

## Scope

### In

- **Per-request cookie scheme.** Replace `cfg.SecureCookies()` at every cookie
  set/clear site with a helper that inspects the actual request:
  `r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"`. Affects
  `vac_session`, `vac_csrf`, and `vac_pre`.
- **Trust `X-Forwarded-Proto` from vac-proxy.** Documented trust boundary: vac-api
  is reached either directly on its host-published port or via vac-proxy. Do
  not stack another reverse proxy in front that rewrites/strips XFP. (A
  follow-up `VAC_TRUSTED_PROXY` switch is recorded as out-of-scope below.)
- **Dashboard Caddy route.** When `VAC_BASE_DOMAIN` is set, the proxy manager
  prepends a system route: host = control-plane domain → upstream
  `vac-api:3000`. Health-checked the same way app routes are.
- **`VAC_CONTROL_DOMAIN` config knob.** Default to `vac.<BaseDomain>` so the
  apex is left free for an app or a marketing page. Overridable to any
  hostname the operator owns (apex included).
- **UI plain-HTTP banner.** When `window.location.protocol === 'http:'`, the
  app shell shows a dismissible warning explaining traffic is unencrypted and
  linking to the `vac set-domain` docs.
- **`vac set-domain` UX update.** The host CLI prints the dashboard URL it
  will be reachable at and the DNS records to create (`A vac.<domain>` plus
  the existing `A *.<domain>` for apps).

### Out (deferred)

- **Phase 3 — IP-only HTTPS via a Let's Encrypt IP certificate (deferred).**
  Caddy obtains a real LE cert for the host's public IPv4 using LE's
  short-lived certificate profile; first-boot prints `https://<ip>` as the
  preferred dashboard URL. Tracked but not implemented in this milestone — it
  depends on Caddy's short-lived-profile config story stabilizing and on
  validating the 6-day renewal cadence in our deployment. See § Phase 3 —
  Deferred below for the shape it should take.
- **`VAC_TRUSTED_PROXY` switch.** Gating XFP trust on a source-IP or
  upstream-marker is reasonable hardening but unnecessary for v1 — vac-proxy
  is the only thing in front of vac-api in the bundled architecture.
- **Closing the host-published `:3000` port behind a domain.** Once the
  dashboard is on HTTPS, the host port becomes a fallback for recovery
  (operator locked out of DNS, ACME stuck, etc.). Tightening it to
  docker-network-only is a separate hardening pass.
- **Forced HTTP→HTTPS redirect on the control-plane host.** Caddy's automatic
  HTTPS already handles this once the route is in place; nothing extra to do.
- **CSRF / Origin checks based on scheme.** The existing CSRF middleware is
  scheme-agnostic and stays that way.

---

## Phases

### Phase 1 — Cookies follow the request scheme

The minimum that unblocks first-boot. Implementable in roughly 10 lines plus
tests.

#### Changes

- `api/internal/server/handler/cookies.go`: introduce
  `secureForRequest(r *http.Request) bool` and pass it (or call it inline) at
  every `http.SetCookie` site. Three cookies in this file: `vac_session`,
  `vac_csrf`, plus their clear-on-logout counterparts.
- `api/internal/auth/cookies.go`: same for `vac_pre`.
- Drop `cfg.SecureCookies()` callers from the cookie paths. Leave
  `Exposure` itself alone — it still drives the WebSocket Origin
  enforcement (`api/internal/server/server.go:208-214`) and is the right
  knob for that.
- `ui/src/components/layout/app-shell.tsx` (or sibling): render a top banner
  when `window.location.protocol === 'http:'`. Copy: *"You're on plain HTTP —
  sessions are insecure. Configure a domain with `vac set-domain` to enable
  HTTPS."* Dismissible (localStorage), one-time per session.

#### Tests

- Unit: `secureForRequest` returns `true` when `r.TLS != nil`, `true` when
  `XFP=https`, `false` otherwise. Table-driven.
- Integration / handler test: a login response served over plain HTTP carries
  cookies without the `Secure` attribute; over TLS (or with XFP=https) it has
  it.

#### Exit criteria

- `make build-api && ./api/bin/vac-api` running on a Linode/EC2 VM, reached
  over `http://<ip>:3000`, accepts a login and the next request is
  authenticated (cookie persisted in browser).
- The banner shows in the UI over HTTP and disappears over HTTPS.

### Phase 2 — `vac set-domain` puts the dashboard on HTTPS

Depends on Phase 1 (Caddy will set `X-Forwarded-Proto: https` and the cookie
helper needs to honor it).

#### Changes

- `api/internal/config/config.go`: add `ControlDomain` field, loaded from
  `VAC_CONTROL_DOMAIN`. Default value (computed when BaseDomain is set):
  `vac.<BaseDomain>`. Empty when BaseDomain is empty.
- `api/internal/proxy/manager.go`: when reconciling routes, prepend a
  control-plane route for `ControlDomain` → `vac-api:3000`. Reuse the existing
  health-check / route-sync paths so the route inherits TLS-issuance
  behavior (on-demand or upfront, per current config).
- `api/internal/caddy/config.go` / `api/internal/server/handler/`: ensure the
  `/internal/caddy/ask` allowlist includes `ControlDomain`.
- `scripts/install.sh` (embedded `vac` CLI, `set-domain` case): after writing
  `VAC_BASE_DOMAIN` and restarting vac-api, print:

  ```
  Base domain set to example.com.
  Dashboard will be reachable at:  https://vac.example.com
  DNS records to create:
    A   vac.example.com   → this host
    A   *.example.com     → this host   (for deployed apps)
  ```

- Update the README / `docs/deployment.md` snippets that quote the old flow.

#### Tests

- Unit: `Config.ControlDomain` defaults to `vac.<BaseDomain>`, accepts an
  override, returns empty when BaseDomain is empty.
- Unit: proxy.Manager builds a route for `ControlDomain` when present, omits
  it when BaseDomain is empty.
- Manual e2e: install on a VM, `vac set-domain example.com`, point
  `vac.example.com` at the host, wait for cert issuance, log in over
  `https://vac.example.com`, confirm cookies have `Secure` set and login
  works.

#### Exit criteria

- Hitting `https://vac.<domain>` after a fresh `vac set-domain` returns the
  dashboard with a valid LE certificate and a working login.
- `http://vac.<domain>` redirects to HTTPS (free with Caddy's automatic
  HTTPS).
- `http://<ip>:3000` continues to work as a recovery path.

### Phase 3 — IP-only HTTPS via a Let's Encrypt IP certificate (deferred)

Not in this milestone. Captured here so the eventual implementer has the same
shape in mind.

Since mid-2025, Let's Encrypt issues certificates whose SAN is an IP address,
delivered via the **short-lived certificate profile** (~6-day lifetime,
auto-renewed by Caddy). This means a freshly-installed VAC on a public VPS
can serve `https://<vps-ip>` with a real, browser-trusted cert and no DNS
involved at all.

- On first boot when `VAC_BASE_DOMAIN` is unset, the installer detects the
  host's primary public IPv4 (best-effort: `ip route get 1`, falling back to
  an external IP-echo service if asked) and prints:

  ```
  Dashboard:  https://<ip>             (HTTPS, may take ~30s for the cert)
              http://<ip>:3000         (plain HTTP fallback / recovery)
  ```

- vac-proxy's Caddy config gains a route for the literal IP host → upstream
  `vac-api:3000`, with TLS configured to use LE's short-lived profile
  (`tls { issuer acme { profile shortlived } }` or the equivalent
  directory URL on the Caddy version we ship). The ask-hook allowlists
  the host's public IP.
- Challenge is `tls-alpn-01` on :443 (already published by vac-proxy). No
  HTTP-01 fallback needed; if :443 is unreachable from LE, the operator is
  going to need a domain regardless.
- A config flag like `VAC_PUBLIC_IP=auto|<ip>|off` controls the behavior.
  Default `auto` — detect once at install and write it into `.env`; the
  operator can disable it explicitly with `off` for hosts behind CGNAT, in a
  corporate lab, or any setup where :443 isn't world-reachable.
- The route is **idempotent against `vac set-domain`**: setting a base
  domain doesn't tear down the IP route; both keep working until the
  operator explicitly opts out. (The dashboard banner from Phase 1 stays
  honest — it's only shown when the *current* `window.location.protocol`
  is plain HTTP, so visiting via the IP HTTPS URL clears it.)
- Open questions before implementing:
  - Confirm the Caddy version VAC ships supports short-lived profile config
    cleanly; if not, bump the proxy image first.
  - Decide whether to also offer the same IP-cert flow for an IPv6 address
    (probably yes, costs nothing extra once the v4 path works).
  - What happens when the VPS IP changes (e.g. snapshot restore on a new
    instance): detect on boot, re-issue, log a warning. Operators rebuilding
    a host should expect a few minutes of "browser can't verify" before the
    new cert lands.

---

## Trade-offs and decisions

- **`vac.<domain>` vs apex as the dashboard default.** Chosen: subdomain.
  Reasoning: the apex is the most-requested place for an operator's marketing
  page or their headline app; making it the *de facto* admin URL pre-empts
  that without warning. `VAC_CONTROL_DOMAIN=<apex>` is one env var away for
  anyone who disagrees.
- **Per-request `Secure` flag vs always-on once HTTPS is configured.** Chosen:
  per-request. It's the simplest correct behavior, requires no global state,
  and naturally degrades when the operator hits the host port directly for
  recovery. The cost is trusting `X-Forwarded-Proto`, which is fine inside the
  bundled topology.
- **Unconditional XFP trust.** Acceptable for v1: vac-proxy is the only
  reverse proxy in the bundled deployment. Documented as a trust boundary; a
  `VAC_TRUSTED_PROXY` knob can come later if external requests arrive.
- **Keep the host port published.** Even after Phase 2, `:3000` remains
  reachable for recovery. Tightening this is a separate hardening pass; doing
  it here would lock operators out the first time DNS or ACME misbehaves.
- **No first-boot self-signed cert.** Considered and rejected. Caddy's
  internal CA gives a "your connection is not private" page; we'd be trading
  one broken-looking experience (sessions silently dropping) for another
  (browser warning). Plain HTTP plus a banner is more honest and gets out of
  the way once Phase 3 ships real LE IP certs.
- **LE IP certs vs nip.io for Phase 3.** Chosen: LE IP certs. The nip.io path
  (a `<ip>.nip.io` wildcard wrapping the IP) is the older trick that worked
  before LE supported IP SANs; it relies on a third-party DNS service in the
  trust path, has a `VAC_ALLOW_NIPIO`-shaped opt-in cost, and gains nothing
  IP certs don't. The only edge case where nip.io still wins is hosts where
  port 443 isn't reachable from LE — and in that case neither path works
  reliably, so the operator needs a domain regardless.

---

## Risks

- **Operator already runs another reverse proxy in front of vac-proxy.** They'd
  break themselves the moment we start honoring `X-Forwarded-Proto`. Mitigate
  by documenting it in `docs/deployment.md` and printing the trust note from
  `vac set-domain`. A `VAC_TRUSTED_PROXY` switch is the long-term answer.
- **DNS hasn't propagated yet when the operator first visits
  `https://vac.<domain>`.** Caddy's on-demand TLS will retry; the user might
  see one failed handshake. Mitigation: the `vac set-domain` output is
  explicit about the DNS records and that propagation can take a few minutes.
- **Existing sessions across a redeploy that flips the Secure flag.** Browsers
  may keep old cookies around. Not a real issue — sessions are server-side
  validated and the old cookie is overwritten on the next response.

---

## Files touched (anticipated)

Phase 1:
- `api/internal/server/handler/cookies.go`
- `api/internal/auth/cookies.go`
- `api/internal/server/handler/cookies_test.go` (new)
- `ui/src/components/layout/app-shell.tsx` (banner)

Phase 2:
- `api/internal/config/config.go`
- `api/internal/proxy/manager.go`
- `api/internal/caddy/config.go` and/or the ask-hook allowlist
- `scripts/install.sh` (the embedded `vac` CLI `set-domain` case)
- `docs/deployment.md`
- `README.md` (the dashboard URL snippet)

Phase 3 (deferred): `scripts/install.sh` (public-IP detection on install,
first-boot URL), `api/internal/proxy/manager.go` (IP-host route on the
short-lived TLS profile), `api/internal/caddy/config.go` (ask-hook IP
allowlist), `api/internal/config/config.go` (new `VAC_PUBLIC_IP` knob),
and possibly a Caddy image bump in `proxy/Dockerfile` if the version we ship
doesn't yet expose short-lived-profile config.
