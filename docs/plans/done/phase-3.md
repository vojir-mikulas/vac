# Phase 3 — Reverse Proxy & HTTPS

## Goal

Put a Caddy reverse proxy in front of the app stacks that Phase 2 already deploys, so that a
running service becomes reachable on a real hostname over HTTPS without the operator touching
a config file. By the end of Phase 3 you can:

1. Set `VAC_BASE_DOMAIN=vac.example.com`, deploy an app `blog`, and reach it at
   `https://blog.vac.example.com` with a valid certificate — no per-app DNS or config.
2. Add a custom domain (`www.acme.com`) to a service via the API; Caddy obtains a cert and
   starts routing within seconds, alongside the automatic subdomain.
3. Delete an app (or remove a domain) and watch the route and its cert tidy up.
4. Restart `vac-api` or recreate the Caddy container and have all routes rebuilt from the DB —
   the proxy state is reconciled from Postgres, not hand-held.
5. See a per-service request-rate sparkline backed by a rolling 24h window in Postgres.

No WebSocket streaming, no notifications, no dashboard — those are Phases 4 and 5. Phase 3
produces the **edge layer** and the REST surface for domains and request metrics. Everything
is still driven by curl against the API.

Reference: see `mvp.md` § Build Phases → Phase 3, § Caddy Integration, § Automatic Subdomains,
and § Caddy Integration → Request metrics for original scope. This document sequences that
scope, picks the routing mechanism, resolves the TLS strategy, and defines exit criteria.

---

## Scope

### In

- `vac-proxy` (Caddy v2) added to the bootstrap compose stack, ports `80`/`443`, admin API on
  the internal compose network only
- Caddy Admin API client: load base config on boot, idempotent per-route `PUT`/`DELETE` via
  `@id` handles, config read-back for reconciliation
- Base Caddy config managed by VAC: one HTTP server on `:80`/`:443`, `automatic_https` on,
  Prometheus metrics enabled, JSON access log to a shared volume
- `domains` table — many hostnames per service, typed `auto` | `custom`, hostname-unique
- Automatic subdomain assignment: on app create (when `VAC_BASE_DOMAIN` is set) each
  HTTP-exposing service gets `{app-slug}.{base-domain}` (or `{service}.{app-slug}.{base}` for
  multi-service apps)
- Custom domain endpoints: add/list/delete a custom hostname per service; both auto and custom
  hostnames route simultaneously
- A long-lived `vac-edge` Docker network: Caddy joins it permanently; each HTTP-exposing app
  container is attached to it on deploy with a deterministic alias `{slug}--{service}`, so
  Caddy reaches upstreams by name on a private network — **no host port publishing required**
- Route sync wired into the deploy pipeline: the app is attached to `vac-edge`, routes (with
  Caddy active health checks) are pushed, and the service is gated to `running` only once Caddy
  reports the upstream healthy; on stop/delete the routes are pulled and the network detached
- Health check moves from VAC's own loopback probe (Phase 2) to **Caddy active upstream health
  checks**, with VAC reading upstream status from the admin API to gate deploys
- Boot-time reconciliation: rebuild the full Caddy route set **and** re-attach live app
  containers to `vac-edge` from the `domains`/`services` tables, so the proxy survives a Caddy
  or API restart
- TLS automation: per-host certificates via Caddy `automatic_https` + ACME HTTP challenge,
  gated by an on-demand-TLS **ask** endpoint so only VAC-known hostnames get certs
- Request metrics: tail Caddy's JSON access log, aggregate request counts per hostname into
  10-second buckets, map host → service via `domains`, store a rolling 24h window in
  `request_metrics`, prune the tail nightly
- `GET /api/apps/:id/metrics` and `GET /api/apps/:id/services/:name/metrics` — request-rate
  time series for the dashboard to consume in Phase 5

### Out (deferred to later phases)

- True wildcard certificate via ACME **DNS** challenge — requires a Caddy image built with a
  DNS-provider plugin and provider credentials; documented as an opt-in below, not the default
  path (per-host on-demand certs cover the MVP exit criteria)
- WebSocket hub, live log/stat streaming (Phase 4)
- Notifications on cert-issued / domain-error events (Phase 4)
- Any dashboard rendering of domains or the request-rate series (Phase 5)
- Forcibly stripping or rewriting `ports:` out of user-supplied compose files — VAC routes via
  `vac-edge` regardless of whether the user also publishes; we recommend `expose` over `ports`
  but do not forbid publishing (post-MVP lint/warning at most)
- Host-level CPU/RAM/disk stats and the `/metrics` scrape for those (Phase 4 stats path)
- Per-app rate limiting / WAF at the edge (post-MVP)

---

## Key technical decisions

### Caddy is configured exclusively through its Admin API, never a Caddyfile

VAC owns the proxy config. On boot VAC `POST`s a minimal base config to
`{admin}/load` (a single HTTP server, automatic HTTPS, metrics, access log) and thereafter
manipulates only the dynamic route layer. Every route VAC creates carries an `@id` field
(`vac-route-{domainID}`), which lets us address it directly:

- `PUT  {admin}/id/vac-route-{domainID}` to create/replace one route
- `DELETE {admin}/id/vac-route-{domainID}` to remove one route
- `GET  {admin}/config/apps/http/servers/vac/routes` to read back for reconciliation

This avoids read-modify-write races on the whole config object and makes route operations
idempotent. No Caddyfile is mounted; `caddy_config` volume only persists Caddy's own
autosave. The DB is the source of truth — Caddy is treated as a rebuildable cache of it.

### Upstream routing: a shared `vac-edge` Docker network, no host publishing

Caddy reaches upstreams over a private Docker network rather than the host. This realises the
`mvp.md` § Health check intent ("route by service name on the Docker network") and lets HTTP
services stop publishing ports to the host entirely — the stronger isolation model.

The mechanism avoids the obvious "connect Caddy to every app network" trap (ambiguous DNS when
two apps both have a service literally named `app`, plus reconnect churn). Instead there is
**one** long-lived network:

- `vac-edge` is created once (external network declared in `compose.yaml`); **Caddy joins it
  permanently** via its `vac-proxy` service definition — no per-deploy connect for Caddy.
- On each deploy, after `compose up`, VAC connects every HTTP-exposing app container to
  `vac-edge` with a deterministic alias: `docker network connect --alias {slug}--{service}
  vac-edge <containerID>` (via the `docker network connect` CLI, matching how Phase 2 already
  shells out to docker rather than using the Engine SDK). The `{slug}--{service}` alias is
  globally unique, so there is no cross-app collision and no dependence on the `-1` container
  index suffix.
- Each route's upstream is therefore `{slug}--{service}:{internalPort}`, where `internalPort`
  is the **container** port the app listens on (not a host port). Detach on delete/stop;
  re-attach on every deploy (containers get fresh IDs on redeploy).
- `vac-api` is deliberately **not** joined to `vac-edge` — otherwise any user container could
  reach `http://vac-api:3000`. The API stays off the data-plane network and drives everything
  through the Docker socket and the Caddy admin API.

This needs a small schema addition (`services.internal_port`) and a boot-time reconcile that
re-attaches live containers, but it removes host port publishing as a requirement and gives
real network isolation between the edge and the host.

### Health check: Caddy active upstream checks, read back via the admin API

Because `vac-api` is intentionally off `vac-edge`, it can no longer probe the container on a
loopback host port the way Phase 2 did. Health authority moves to **Caddy**, which already
wants to health-check anything it proxies:

- Each route VAC creates configures `reverse_proxy.health_checks.active` (path from
  `services.health_path`, interval/timeout from config).
- The deploy pipeline gates `→ running` by polling `GET {admin}/reverse_proxy/upstreams` and
  matching the upstream address `{slug}--{service}:{internalPort}`; the service is considered
  healthy when Caddy reports it up (zero recent fails) within `VAC_HEALTH_CHECK_TIMEOUT`.
- This **reorders the pipeline** relative to Phase 2: routing now happens *before* the health
  gate (Caddy must be proxying to the upstream before it can check it), i.e.
  `up → attach vac-edge → sync routes → poll Caddy upstream health → running`.

Portless services (workers, queues, DBs — no `internal_port`) get no route and are considered
healthy automatically, exactly as in Phase 2.

The `VAC_HEALTH_CHECK_TIMEOUT` / `VAC_HEALTH_CHECK_RETRIES` config from Phase 2 is reused —
retries become the number of admin-API polls before declaring the deploy unhealthy.

### TLS: per-host automatic HTTPS with an on-demand "ask" gate, not a wildcard

Caddy's `automatic_https` obtains and renews a certificate per hostname over the ACME HTTP
challenge out of the box — no plugin, no DNS credentials. The risk with on-demand issuance is
that an attacker pointing arbitrary DNS at the VPS could make Caddy attempt unbounded cert
issuance. We close that with Caddy's on-demand TLS `ask` hook: before issuing, Caddy calls
`GET {VAC_INTERNAL}/internal/caddy/ask?domain=<host>` and only proceeds on `200`. VAC answers
`200` iff the hostname exists in the `domains` table, `403` otherwise.

Wildcard subdomain certs (`*.vac.example.com` via one DNS-challenge cert) are **not** the
default because they require a custom Caddy build containing the operator's DNS-provider
plugin plus API credentials. When `VAC_BASE_DOMAIN` is set without DNS-provider config, each
automatic subdomain simply gets its own per-host cert on first request — functionally
identical from the user's perspective, just N certs instead of one wildcard. The wildcard path
is documented as opt-in (provide a DNS-plugin image + `VAC_ACME_DNS_PROVIDER` config).

### Per-service request rate comes from the access log, not `/metrics`

`mvp.md` § Request metrics says "scrape `localhost:2019/metrics` and map upstream labels to
services." In practice Caddy's Prometheus metrics (`caddy_http_requests_total`) are **not**
labelled by request host or upstream — only by `server`, `handler`, `code`, `method`. There
is no way to attribute a request to a specific app/service from the default metric set.

So per-service attribution comes from Caddy's structured **access log**: VAC enables JSON
access logging to a shared volume (`vac_caddy_logs`), tails it, and aggregates
`request.host` + `status` into 10-second buckets in memory, flushing each bucket to
`request_metrics` keyed by the service the hostname maps to. The Prometheus `/metrics`
endpoint is still scraped, but only for host-level aggregate request rate (Phase 4 stats),
not per-service breakdown. This is the one architectural correction relative to the mvp text,
and it is the only approach that yields the per-service sparkline the UI calls for.

---

## Library decisions

| Concern | Pick | Why |
|---|---|---|
| Caddy Admin API client | stdlib `net/http` + `encoding/json` | The Admin API is plain JSON over HTTP; the route/config objects are small structs we own. No SDK exists worth pulling in. |
| Caddy config structs | hand-written Go structs in `internal/caddy/config.go` | We emit a tiny subset of Caddy's JSON schema (server, route, `reverse_proxy` handler + active health check, `host` matcher). Modelling only what we write keeps it legible. |
| Docker network management | `docker network` CLI via `os/exec` (internal/dockercli) | `connect` / `disconnect` / `create` for `vac-edge`; matches the codebase's existing CLI-only approach (Phase 2 shells out to docker, not the Engine SDK) — zero new deps. |
| Access-log tailing | `github.com/nxadm/tail` | Battle-tested follow-with-rotation; reimplementing inode-watch + truncation handling by hand is a known foot-gun. (Fall back to a hand-rolled `os.Seek` poller if we want zero new deps — noted in M6.) |
| Prometheus text parse (host aggregate) | `github.com/prometheus/common/expfmt` | Already transitively present via the docker client tree; correct exposition-format parsing beats a regex. |
| Hostname validation | `golang.org/x/net/idna` + stdlib | Punycode/IDNA normalisation and basic label rules for custom domains. |
| Caddy image | `caddy:2-alpine` (official) | No plugins needed for the default HTTP-challenge path. Wildcard opt-in swaps in a `caddy-dns/<provider>` build. |

**Not adopting:**

- A separate Prometheus instance — VAC scrapes Caddy directly (per mvp); host aggregate only.
- `caddyserver/caddy` as a Go dependency — we talk to it over HTTP, we don't embed it.
- A message queue for log lines — the access-log tailer aggregates in memory and flushes on a
  ticker; volume is bounded by request rate and we only keep 10s buckets.

---

## File layout (additions in Phase 3)

```
compose.yaml                              # + vac-proxy service, external vac-edge network, caddy_data/caddy_config/vac_caddy_logs volumes
api/
├── internal/
│   ├── caddy/
│   │   ├── client.go                     # Admin API: Load, PutRoute, DeleteRoute, GetRoutes, Upstreams, health
│   │   ├── config.go                     # base-config + Route/Matcher/Handler/ActiveHealthCheck structs
│   │   └── client_test.go                # against an httptest stand-in for the admin API
│   ├── proxy/
│   │   ├── manager.go                    # domain → route translation, Sync(appID), Reconcile(ctx)
│   │   ├── network.go                    # vac-edge ensure/connect/disconnect, upstream alias derivation
│   │   ├── health.go                     # poll Caddy /reverse_proxy/upstreams to gate a deploy
│   │   ├── hostname.go                   # auto-subdomain derivation + custom hostname validation
│   │   └── manager_test.go
│   ├── reqmetrics/
│   │   ├── tailer.go                     # tail caddy access log → per-host 10s buckets
│   │   ├── aggregator.go                 # bucket flush → store.AppendRequestMetrics
│   │   ├── scraper.go                    # /metrics scrape → host aggregate (Phase 4 seam)
│   │   └── reqmetrics_test.go
│   ├── store/
│   │   ├── domains.go
│   │   └── request_metrics.go
│   ├── db/migrations/
│   │   ├── 00012_domains.sql
│   │   ├── 00013_request_metrics.sql
│   │   └── 00014_services_internal_port.sql   # + internal_port, health_path; drop services.domain
│   └── server/handler/
│       ├── domains.go                    # GET app domains, POST/DELETE custom domain
│       ├── caddy_ask.go                  # GET /internal/caddy/ask (unauthenticated, network-gated)
│       └── metrics.go                    # GET request-rate series per app / per service
```

`internal/proxy` is the orchestration seam (knows about `store`, `caddy`, the Docker client,
and config); `internal/caddy` is a dumb transport that knows only Caddy's JSON.
`internal/reqmetrics` depends on `store` and config but not on `proxy`. Handlers stay thin and
call into `proxy` and `store` directly, matching the Phase 2 convention.

---

## Data model additions

### `domains` (00012)

```
id            UUID PK DEFAULT gen_random_uuid()
app_id        UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE
service_name  TEXT NOT NULL
hostname      TEXT NOT NULL                       -- normalised, lower-case, punycode
type          TEXT NOT NULL CHECK (type IN ('auto','custom'))
cert_status   TEXT NOT NULL DEFAULT 'pending'     -- 'pending'|'active'|'error', informational
created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
UNIQUE (hostname)
FOREIGN KEY (app_id, service_name) REFERENCES services(app_id, service_name) ON DELETE CASCADE
```

`hostname` is globally unique — one hostname routes to exactly one service. The composite FK
ties a domain to a concrete service row and cascades cleanup when a service disappears from the
compose project. `cert_status` is advisory only (polled best-effort from Caddy's config/PKI
read-back); routing does not depend on it.

**Migration of the Phase 2 placeholder:** `services.domain` (added in 00006, unused) is
back-filled into `domains` as `type='custom'` for any non-null value, then the column is
dropped. The composite FK requires a `UNIQUE (app_id, service_name)` on `services`, which 00006
already provides.

Index on `(app_id)` for the per-app domain list.

### `request_metrics` (00013)

A pre-aggregated rolling window — one row per (service, 10s bucket). Not raw request rows.

```
id            BIGSERIAL PK
app_id        UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE
service_name  TEXT NOT NULL
bucket_ts     TIMESTAMPTZ NOT NULL                -- floor(ts, 10s), bucket start
requests      INT NOT NULL DEFAULT 0
errors        INT NOT NULL DEFAULT 0              -- count of status >= 500
bytes_out     BIGINT NOT NULL DEFAULT 0
UNIQUE (app_id, service_name, bucket_ts)
```

Index on `(app_id, bucket_ts DESC)` for the read path. Upsert on the unique key so a late log
line for an open bucket increments rather than duplicates. Pruned to 24h by the retention
goroutine (extends the Phase 2 pruner rather than a new one).

### `services` changes (00014)

Network routing needs the **container** port (what the app listens on), not the host-published
port Phase 2 captured. We also let each service declare a health-check path for Caddy's active
check.

```
ALTER TABLE services ADD COLUMN internal_port INT;     -- container port for vac-edge routing
ALTER TABLE services ADD COLUMN health_path   TEXT;    -- active health-check path, default '/'
ALTER TABLE services DROP COLUMN domain;               -- placeholder; superseded by the domains table
```

`internal_port` is populated from the `TargetPort` of the first published/exposed port in
`docker compose ps --format json` Publishers; when the compose neither publishes nor `expose`s
a port, the operator sets it (and `health_path`) via the existing
`PATCH /api/apps/:id/services/:name` endpoint. `exposed_port` (host port) stays for backward
compatibility and diagnostics but is no longer on the routing path. The `services.domain`
back-fill that Phase 2's column was a placeholder for happens in 00012 (into the `domains`
table) **before** this drop — keep the migration order so the data survives.

---

## Sequence

### M1 — Caddy container + Admin client + base config on boot

**Goal:** `vac-proxy` runs, VAC can talk to its admin API, and a known-good base config is
loaded on every boot.

- `compose.yaml`: add `vac-proxy` (`caddy:2-alpine`):
  - `ports: ["80:80", "443:443", "443:443/udp"]`
  - `networks: [default, vac-edge]` — permanently joined to the shared upstream network so app
    containers attached later are reachable by alias
  - volumes: `caddy_data:/data` (certs/ACME state — must persist), `caddy_config:/config`
    (Caddy autosave), `vac_caddy_logs:/var/log/caddy` (shared, also mounted read-only into
    `vac-api`)
  - **No** published `2019` — the admin API stays on the internal compose network only
  - start with `caddy run --resume` so a restart re-loads the last config until VAC re-pushes
- `compose.yaml` top level: declare `vac-edge` as an **external** network (created by VAC on
  boot via `NetworkCreate` if absent — see M4 — so it exists before either container starts;
  alternatively `docker network create vac-edge` is a documented prerequisite)
- `vac-api` env: `VAC_CADDY_ADMIN_URL=http://vac-proxy:2019`, `VAC_EDGE_NETWORK=vac-edge`; mount
  `vac_caddy_logs` read-only. `vac-api` is **not** on `vac-edge` (see Health-check decision)
- `internal/caddy/client.go`: `Load(ctx, cfg)`, `PutRoute(ctx, id, route)`,
  `DeleteRoute(ctx, id)`, `GetRoutes(ctx)`, `Ping(ctx)` — thin JSON-over-HTTP wrappers with a
  timeout context and typed errors (`ErrCaddyUnavailable`)
- `internal/caddy/config.go`: `BaseConfig(opts)` builds the minimal config — `admin.listen
  ":2019"` (so it's reachable from `vac-api` over the compose network; document that it must
  never be published), one HTTP server named `vac` listening `:80`/`:443` with
  `automatic_https` enabled, `metrics` enabled, and a JSON access log to
  `/var/log/caddy/access.log`
- `main.go`: after migrations, `caddy.Load(baseConfig)`; if Caddy is unreachable, log a single
  warning and continue (mirrors the Phase 2 docker-socket soft-probe — VAC must boot on a
  misconfigured host so the operator can fix it)

**Test:** integration — bring up the stack, assert `GET {admin}/config/` returns the server
named `vac`; `client_test.go` drives `PutRoute`/`DeleteRoute`/`GetRoutes` against an
`httptest` server emulating the `/id/` and `/config/` endpoints.

### M2 — Migrations + store for domains and request metrics

**Goal:** the two new tables exist, are applied on boot, and have a thin store layer.

- Add migrations `00012_domains.sql`, `00013_request_metrics.sql`, and
  `00014_services_internal_port.sql` per the Data Model section. Order matters: 00012 back-fills
  `services.domain` into `domains`; 00014 then drops the column and adds `internal_port` +
  `health_path`
- Extend the Phase 2 `docker compose ps` parse + `services.Upsert` to capture `TargetPort` into
  `internal_port` and update the `services` store accessors / `PATCH` handler to read/write
  `internal_port` and `health_path`
- `internal/store/domains.go`: `CreateDomain`, `ListDomainsByApp`, `GetDomainByHostname`,
  `DeleteDomain`, `ListAllDomains` (for boot reconcile), `SetCertStatus`
- `internal/store/request_metrics.go`: `UpsertRequestBucket(rows []Bucket)` (batched multi-row
  `INSERT ... ON CONFLICT DO UPDATE SET requests = request_metrics.requests + EXCLUDED...`),
  `QueryRequestSeries(appID, service, since)`, `PruneRequestMetrics(olderThan)`
- Extend `store_integration_test.go` with a round-trip per table, including the upsert-increment
  path and the unique-hostname rejection

**Test:** integration — insert a domain, read by hostname; upsert the same bucket twice and
assert the counter summed; assert duplicate-hostname insert returns a unique-violation.

### M3 — Domain model: auto-subdomain assignment + custom-domain endpoints

**Goal:** apps get an automatic hostname when `VAC_BASE_DOMAIN` is set, and users can attach
custom domains via the API.

- `internal/proxy/hostname.go`:
  - `AutoSubdomain(appSlug, serviceName string, baseDomain string, multiService bool) string` —
    `{slug}.{base}` for the primary/only HTTP service, `{service}.{slug}.{base}` when an app
    exposes more than one HTTP service (avoids collisions)
  - `NormalizeHostname(raw string) (string, error)` — lower-case, trim, IDNA/punycode encode,
    reject wildcards, ports, paths, and the bare base domain
- Auto assignment hook: when `VAC_BASE_DOMAIN` is set, the deploy pipeline (M5) inserts an
  `auto` domain row for each HTTP-exposing service that lacks one. (HTTP-exposing = has
  `internal_port`; portless workers/DBs get no hostname and no route.)
- Handlers (`internal/server/handler/domains.go`), under the auth-required `apps` group:
  - `GET    /api/apps/:id/domains` — list all domains across the app's services
  - `POST   /api/apps/:id/services/:name/domains` — body `{ "hostname": "www.acme.com" }`,
    validates, inserts `type='custom'`, triggers a route sync (M4); rejects duplicates with
    `409`
  - `DELETE /api/apps/:id/domains/:domainId` — removes the row and its Caddy route; refuses to
    delete the last `auto` domain only if the operator explicitly asks to keep auto on (simple
    rule: `auto` domains are managed by VAC and deleted only via app deletion; custom domains
    are freely add/remove)

**Test:** unit — `AutoSubdomain` table-driven for single/multi-service and unset base domain;
`NormalizeHostname` for punycode, uppercase, wildcard-reject, path-reject. Handler — POST a
custom domain then GET lists both auto + custom; POST a dupe returns 409.

### M4 — `vac-edge` network + route synchronisation into Caddy

**Goal:** app containers are reachable by Caddy over a private network, and the `domains` table
is projected into Caddy's route set idempotently and rebuilt on boot.

- `internal/proxy/network.go`:
  - `EnsureNetwork(ctx)` — `NetworkCreate` `vac-edge` if absent (idempotent); called on boot
  - `Attach(ctx, containerID, alias)` — `docker network connect` the container to `vac-edge` with
    network alias `{slug}--{service}`; ignore "already attached" as success
  - `Detach(ctx, containerID)` — `NetworkDisconnect`, ignore "not attached"
  - `alias(slug, service) string` → `{slug}--{service}` (the upstream DNS name)
- `internal/proxy/manager.go`:
  - `routeFor(domain, service) caddy.Route` — a `host` matcher on `domain.hostname` and a
    `reverse_proxy` handler with upstream `{slug}--{service}:{service.internal_port}` plus an
    `health_checks.active` block (`path = service.health_path or "/"`, interval/timeout from
    config); the route's `@id` is `vac-route-{domain.id}`
  - `Sync(ctx, appID)` — ensure each of the app's HTTP containers is attached to `vac-edge`,
    then for every domain `PutRoute` (create-or-replace) and `DeleteRoute` for domains removed
    since last sync. Called after a successful deploy, on custom domain add/delete, and on app
    stop (stop pulls routes and detaches)
  - `Reconcile(ctx)` — on boot: `EnsureNetwork`; list **all** domains from the DB; for each,
    re-`Attach` the current container (looked up from `services.container_id`) and `PutRoute`;
    then read Caddy's current routes and `DeleteRoute` any `vac-route-*` id not backed by a DB
    row (handles routes orphaned by a crash between DB delete and Caddy delete)
  - All Caddy/network calls are best-effort with retry/backoff against `ErrCaddyUnavailable`; a
    failed sync marks `cert_status='error'` and logs, but never tears down a running stack
    (routing is eventual)
- `main.go`: call `proxy.Reconcile` after `caddy.Load` on boot

**Test:** unit — `routeFor` produces the expected JSON (alias upstream + active health check)
for single/multi domain; `Sync` against a fake `caddy.Client` + fake docker client asserts the
correct attach + `PutRoute`/`DeleteRoute` calls for an add and a remove. Integration — deploy a
fixture HTTP service with `VAC_BASE_DOMAIN` set, `curl -H "Host: blog.localtest.me"
http://vac-proxy` returns the app's response (proving Caddy reached it over `vac-edge` with no
host port published).

### M5 — Pipeline reorder, Caddy-gated health, lifecycle

**Goal:** routing is automatic across the full app lifecycle, and the deploy gate now relies on
Caddy's active health checks rather than the Phase 2 loopback probe.

- Reorder `internal/deploy/pipeline.go`. Phase 2 was `up → (own HTTP health probe) → running`.
  Phase 3 becomes:
  ```
  up
    → upsert services (now also captures internal_port)
    → assign auto domains for HTTP services (if VAC_BASE_DOMAIN set)
    → proxy.Sync(appID)            # attach to vac-edge + PutRoute with active health checks
    → proxy.WaitHealthy(appID)     # poll Caddy /reverse_proxy/upstreams until up or timeout
    → running
  ```
  The Phase 2 `internal/deploy/healthcheck.go` loopback prober is **removed** from the deploy
  path (kept only if any non-routed check is still wanted; default: deleted).
- `internal/proxy/health.go`: `WaitHealthy(ctx, appID)` — poll `GET
  {admin}/reverse_proxy/upstreams`, match each HTTP service's upstream address
  `{slug}--{service}:{internal_port}`, succeed when all report up (no recent fails) within
  `VAC_HEALTH_CHECK_TIMEOUT`; `VAC_HEALTH_CHECK_RETRIES` bounds the poll count. A service whose
  Caddy active check never passes flips to `degraded` (stack is up but not serving), matching
  the Phase 2 status semantics. **Distinguish** this from the M4 best-effort rule: a *routing
  push* failure is eventual/non-fatal; a *health* failure is a real deploy outcome.
- Extend stack control (`internal/server/handler/stack_control.go`):
  - `stop` / app delete → `proxy.Sync` which now also **detaches** the app's containers from
    `vac-edge` and drops their routes, so a stopped app returns a clean 502/503 rather than
    proxying to a dead upstream (for a temporary stop the `domains` rows are kept; for delete
    they cascade away)
  - `start` / `restart` → `proxy.Sync` + `WaitHealthy` after the stack is back
- App delete handler removes `domains` rows (cascade via the app FK), detaches containers, and
  calls `proxy.Sync` to purge routes

**Test:** integration — deploy → reachable via Caddy and the deploy only reaches `running` once
Caddy reports the upstream healthy (assert a slow-starting fixture stays `deploying` until its
`/` answers); `POST .../stop` → host returns 502/503 and the container is off `vac-edge`;
`POST .../start` → reachable again; `DELETE /api/apps/:id` → route gone (`GetRoutes` no longer
lists the id) and container detached.

### M6 — TLS automation + on-demand ask gate

**Goal:** real certificates are issued automatically, and only for hostnames VAC knows.

- Base config (M1) sets `automatic_https` on and `tls.automation.on_demand.ask` to
  `http://vac-api:3000/internal/caddy/ask` with a short permission TTL
- `internal/server/handler/caddy_ask.go`: `GET /internal/caddy/ask?domain=<host>` —
  **unauthenticated** (Caddy can't present a session), returns `200` iff
  `store.GetDomainByHostname` finds the host, else `403`. Mounted on the main mux but
  documented as network-internal; it leaks only domain existence (low risk). Optionally bind it
  behind a shared-secret header `X-Caddy-Ask-Token` that VAC injects into the base config.
- Cert status read-back: a light periodic job (or piggy-backed on the metrics ticker) reads
  Caddy's PKI/automation state and updates `domains.cert_status` to `active`/`error` for the UI
- Document the wildcard opt-in: when `VAC_ACME_DNS_PROVIDER` + credentials are supplied and a
  DNS-plugin Caddy image is used, VAC configures a single `*.{VAC_BASE_DOMAIN}` automation
  policy via DNS challenge instead of per-host on-demand. Default path leaves this unset.

**Test:** unit — `ask` returns 200 for a seeded hostname, 403 otherwise, 400 on missing param.
Manual/integration on a real VPS with a public domain: deploy, hit `https://blog.<domain>`,
assert a valid Let's Encrypt cert (staging ACME directory in CI to avoid rate limits).

### M7 — Request metrics: access-log tail → buckets → endpoints

**Goal:** per-service request rate lands in `request_metrics` and is queryable.

- `internal/reqmetrics/tailer.go`: follow `/var/log/caddy/access.log` (shared volume,
  read-only in `vac-api`), parse each JSON line (`request.host`, `status`, `size`), drop lines
  whose host isn't in a cached host→service map (refreshed from `domains` every N seconds)
- `internal/reqmetrics/aggregator.go`: accumulate counts into 10s buckets keyed by
  (app, service, bucket_ts); flush every `VAC_CADDY_METRICS_INTERVAL` via
  `store.UpsertRequestBucket`
- `internal/reqmetrics/scraper.go`: scrape `{admin}/metrics` for host-level aggregate request
  rate only (stored separately or surfaced live in Phase 4 — stub the store call now, leave the
  parse wired)
- Extend the Phase 2 retention pruner to also `PruneRequestMetrics(NOW() - 24h)`
- Handlers (`internal/server/handler/metrics.go`):
  - `GET /api/apps/:id/metrics?since=1h` — summed series across the app's services
  - `GET /api/apps/:id/services/:name/metrics?since=1h` — per-service series
  - response is `[{ ts, requests, errors, bytes_out }, ...]` at 10s resolution
- Start the tailer + aggregator goroutines from `main.go`, both respecting `ctx.Done()`

**Test:** unit — feed synthetic JSON log lines through the tailer/aggregator and assert the
bucket counts and host→service mapping (including a line for an unknown host being dropped).
Integration — deploy, `curl` the app via Caddy 5×, wait one flush interval, assert the series
endpoint reports ≥5 requests in the recent bucket.

### M8 — Hardening pass

- `/health` extended: add a soft Caddy probe (`caddy.Ping`, 1s timeout). Surface as
  `{"db":"ok","docker":"ok","caddy":"ok"}`; Caddy being down is **non-fatal** (degrades to a
  warning field, not `503`) because the app containers keep running on `vac-edge` even if the
  edge proxy is briefly down — only ingress is affected, not the workloads
- Confirm `caddy_data` persistence: recreate `vac-proxy`, assert certs are not re-requested
  (ACME state survived) and routes **and `vac-edge` attachments** are rebuilt by `Reconcile`
- Reconcile is idempotent and safe to run repeatedly: run it twice on boot in a test, assert no
  duplicate routes, no spurious deletes, and no duplicate network attachments
- Verify the admin API is **not** reachable from outside the compose network (no published
  `2019`), and that `vac-api` is **not** on `vac-edge` (a user container cannot reach
  `http://vac-api:3000`); document both security notes prominently
- Verify a deployed HTTP service works with **no host port published** (remove any `ports:`
  from a fixture, keep `expose:`, confirm it still routes through Caddy)
- The only new external I/O is HTTP to Caddy, the Docker socket (network connect/disconnect),
  and the access-log file — verify the log volume is mounted read-only in `vac-api`
- `golangci-lint run ./...` clean
- Manual end-to-end on a real VPS: set `VAC_BASE_DOMAIN`, deploy a multi-service app, confirm
  each HTTP service is reachable at its auto subdomain over HTTPS, add a custom domain, confirm
  both route, delete the app, confirm certs/routes tidy up

---

## Testing strategy

| Layer | Tool | What it covers |
|---|---|---|
| Unit | `go test`, `httptest` | hostname derivation/validation, `routeFor` JSON, `Sync` call sequencing against a fake Caddy client, access-log parse + bucketing, ask-endpoint logic |
| Handler | `httptest.NewRecorder` + chi router | domain add/list/delete status codes & shapes, dupe-hostname 409, metrics series shape |
| Integration (DB) | `testcontainers-go` Postgres | domains + request_metrics round-trips, upsert-increment, unique-hostname rejection |
| Integration (Caddy) | real `caddy:2-alpine` container (or `httptest` admin stub where a real Caddy is unavailable) | base-config load, route put/delete via `@id`, active-health-check config, reconcile orphan cleanup, `Host`-header routing through the proxy |
| Integration (Docker+Caddy) | host Docker daemon + Caddy, `t.Skipf` if absent | `vac-edge` attach/detach, Caddy-gated deploy health, deploy → reachable via Caddy with no host port → stop → 502 + detached → start → reachable → delete → route gone |
| Manual / real VPS | curl + a public domain, ACME **staging** | actual TLS issuance, automatic subdomain + custom domain side-by-side, request-rate series populated |

**ACME in CI:** never hit Let's Encrypt production from CI — point Caddy's automation at the
ACME **staging** directory (or `internal` self-signed issuer) so cert tests don't burn rate
limits. Production issuance is exercised only in the manual VPS pass.

**`localtest.me` / `Host` header:** route-level integration tests send an explicit `Host`
header to `vac-proxy:80` so no real DNS is needed; TLS issuance is the only thing that needs a
real public hostname, and that lives in the manual tier.

---

## Configuration additions

These are documented in `mvp.md` § Configuration but become live in Phase 3:

| Variable | Default | First used by |
|---|---|---|
| `VAC_CADDY_ADMIN_URL` | `http://vac-proxy:2019` | M1 caddy client |
| `VAC_EDGE_NETWORK` | `vac-edge` | M4 network attach / upstream routing |
| `VAC_BASE_DOMAIN` | `` (auto subdomains disabled) | M3 hostname assignment |
| `VAC_CADDY_METRICS_INTERVAL` | `10s` | M7 aggregator flush + scrape |
| `VAC_CADDY_ACCESS_LOG` | `/var/log/caddy/access.log` | M7 tailer |
| `VAC_CADDY_ASK_TOKEN` | auto-generated | M6 ask-endpoint shared secret (optional) |
| `VAC_REQUEST_METRICS_RETENTION` | `24h` | M7 prune (extends Phase 2 pruner) |
| `VAC_ACME_DNS_PROVIDER` | `` (HTTP challenge / per-host) | M6 wildcard opt-in only |
| `VAC_ACME_CA` | `` (Let's Encrypt prod) | M6 / CI override to staging |

Extend `internal/config/config.go` with a `caddy:` block (`admin_url`, `metrics_interval`,
`base_domain`, `access_log`, `edge_network`) and the request-metrics retention, per the schema
in `mvp.md` § Configuration. `VAC_BASE_DOMAIN` already appears in the env reference;
`VAC_EDGE_NETWORK` is new and should be added there too; the rest are new live wirings of
existing planned keys.

---

## Exit criteria

Phase 3 is done when all of these pass on a fresh clone:

- [ ] `docker compose up` brings up vac-db + vac-api + vac-proxy; `/health` returns
      `{"db":"ok","docker":"ok","caddy":"ok"}`
- [ ] On boot VAC loads a base Caddy config (server `vac`, automatic HTTPS, metrics, access log)
      via the Admin API; the admin API is **not** reachable from outside the compose network,
      and `vac-api` is **not** attached to `vac-edge`
- [ ] With `VAC_BASE_DOMAIN` set, deploying app `blog` creates an `auto` domain
      `blog.<base>`, attaches the container to `vac-edge`, and the service is reachable through
      Caddy with **no host port published** (verified via `Host` header)
- [ ] A deploy reaches `running` only after Caddy's active health check reports the upstream
      healthy; a service whose `/` never answers ends `degraded`, not `running`
- [ ] A multi-service app gets distinct `{service}.{slug}.{base}` hostnames, all routing
- [ ] `POST /api/apps/:id/services/:name/domains` adds a custom domain that routes alongside
      the auto subdomain; a duplicate hostname is rejected with `409`
- [ ] `DELETE /api/apps/:id/domains/:domainId` removes the route from Caddy
- [ ] Stopping an app pulls its live routes and detaches it from `vac-edge` (host returns
      502/503); starting it re-attaches and restores routing
- [ ] Deleting an app purges its domains and Caddy routes (cascade + `Sync`) and detaches the
      containers from `vac-edge`
- [ ] Recreating the `vac-proxy` container loses no certs (`caddy_data` persisted) and VAC
      `Reconcile` rebuilds every route **and re-attaches live containers** to `vac-edge` from
      the DB; running reconcile twice creates no duplicate routes or attachments and no spurious
      deletes
- [ ] The on-demand `ask` endpoint returns `200` only for known hostnames, `403` otherwise
- [ ] On a real VPS with a public domain, `https://blog.<domain>` serves a valid certificate
      (manual tier, ACME staging in CI)
- [ ] Hitting an app N times through Caddy populates `request_metrics`; `GET
      /api/apps/:id/services/:name/metrics?since=1h` reports the requests at 10s resolution
- [ ] `request_metrics` rows older than 24h are pruned by the retention goroutine
- [ ] `golangci-lint run ./...` is clean
- [ ] Integration suite passes locally (Postgres + Docker + Caddy tiers)
- [ ] Control plane still idles under 200 MB RAM (vac-api + vac-proxy, excluding Postgres and
      user containers); note Caddy's resident footprint in the PR description
