# Deviations from `mvp.md`

A running log of where the **implementation plans deviate from `mvp.md`**, and why. `mvp.md`
is the north star; this file is the honest record of where we knowingly did something
different so nobody has to reverse-engineer the rationale later.

When a phase plan departs from the mvp, add a row here. Keep each entry: what the mvp said,
what we do instead, why, and the trade-off (what we give up / when we'd revisit).

---

## Phase 3 — Reverse Proxy & HTTPS

### D1 — Per-service request rate comes from the access log, not the Prometheus `/metrics` endpoint

- **mvp.md says** (§ Caddy Integration → Request metrics): scrape `localhost:2019/metrics`
  every 10s and "map the `upstream` labels to app/service names" for per-service request rate.
- **We do instead:** enable Caddy's **JSON access log** to a shared volume, tail it, and
  aggregate `request.host` + `status` into 10-second buckets mapped to a service via the
  `domains` table. `/metrics` is still scraped, but only for host-level aggregate.
- **Why:** Caddy's Prometheus metrics (`caddy_http_requests_total`) are labelled by
  `server`/`handler`/`code`/`method` only — **not** by request host or upstream. There is no
  way to attribute a request to a specific app/service from the default metric set, so the
  mvp approach cannot produce the per-service sparkline it calls for. This is a correction, not
  a preference.
- **Trade-off:** we depend on parsing access-log lines (bounded by request rate; only 10s
  buckets are kept). Negligible at MVP scale.

### D2 — Upstream routing over a shared `vac-edge` Docker network, with no host port publishing

- **mvp.md says** (§ Caddy Integration, § Service Status Model / health note): Caddy routes to
  services; the health note anticipates "switch to the Docker network once Caddy routes by
  service name." The mvp doesn't pin down the exact network topology.
- **We do instead:** one long-lived external network `vac-edge`. Caddy joins it permanently;
  each HTTP-exposing app container is attached on deploy with a deterministic alias
  `{slug}--{service}`, and routes target `{slug}--{service}:{internal_port}`. HTTP services no
  longer need to publish ports to the host.
- **Why:** real network isolation between the edge and the host; no ambiguous cross-network
  DNS (the alias is globally unique); a single permanent network avoids per-deploy juggling of
  the Caddy container across many app networks.
- **Trade-off / cost:** adds a small schema field (`services.internal_port`), per-deploy
  `NetworkConnect`/`Disconnect`, and a boot-time reconcile that re-attaches live containers.
  `vac-api` is deliberately kept **off** `vac-edge` (so user containers can't reach the API),
  which forced D3.
- **Considered and rejected:** routing via the host gateway
  (`host.docker.internal:{hostPort}`) to Phase 2's loopback-published ports. Simpler and kept
  health checks unchanged, but left HTTP services published on the host and gave weaker
  isolation. We chose the stronger model deliberately.

### D3 — Deploy health gating moves to Caddy active health checks

- **mvp.md says** (implied by Phase 2): VAC owns the health check (Phase 2 probes
  `127.0.0.1:{hostPort}` directly).
- **We do instead:** each route configures Caddy `reverse_proxy.health_checks.active`; the
  deploy pipeline gates `→ running` by polling Caddy's `/reverse_proxy/upstreams` admin
  endpoint. The pipeline reorders to `up → attach vac-edge → sync routes → poll Caddy health →
  running`.
- **Why:** a direct consequence of D2 — because `vac-api` is intentionally not on `vac-edge`,
  it can no longer reach the container on a loopback host port. Caddy already health-checks
  what it proxies, so health authority moves there rather than adding a redundant path.
- **Trade-off:** deploy health now depends on the proxy being up. A *routing-push* failure
  stays best-effort/eventual; a *health* failure is a real deploy outcome (`degraded`). The
  Phase 2 loopback prober is removed from the deploy path.

### D4 — TLS via per-host on-demand certificates (HTTP challenge), not a wildcard by default

- **mvp.md says** (§ Automatic Subdomains): "Caddy handles the wildcard TLS certificate via
  ACME DNS challenge" for `*.{VAC_BASE_DOMAIN}`.
- **We do instead:** rely on Caddy `automatic_https` + ACME **HTTP** challenge to issue one
  cert per hostname on demand, gated by an on-demand-TLS `ask` endpoint
  (`GET /internal/caddy/ask`) that only authorises hostnames present in the `domains` table.
  Wildcard-via-DNS-challenge is an **opt-in** (set `VAC_ACME_DNS_PROVIDER` + use a Caddy image
  built with the matching DNS plugin).
- **Why:** a true wildcard requires a custom Caddy build containing the operator's DNS-provider
  plugin plus API credentials — real operator friction the MVP shouldn't mandate. Per-host
  on-demand certs are functionally identical to the end user (just N certs instead of one).
- **Trade-off:** N certs instead of one; each new subdomain triggers an ACME issuance on first
  request (small first-hit latency). Invisible at MVP scale; the wildcard opt-in is the escape
  hatch if an instance grows to hundreds of apps.

---

## Phase 4 — Real-time

### D5 — Host stats land in Phase 4, exposed via `GET /api/host/stats` + a `host` WS topic

- **mvp.md says** (§ API Surface → Real-time): the listed WS endpoints are per-app logs/stats and
  per-deployment build logs; host CPU/RAM/disk is shown on the Global Dashboard (§ UI Structure)
  but no host-stats endpoint is enumerated. Phase 3's plan explicitly deferred host-level stats to
  "the Phase 4 stats path".
- **We do instead:** add `GET /api/host/stats` (snapshot) and a `host` WS topic, sourced from
  `gopsutil` (CPU/RAM/disk) plus the Phase 3 `reqmetrics.Scraper` for the aggregate request rate.
- **Why:** the Phase 5 dashboard needs host vitals and Phase 3 left the scraper seam wired exactly
  for this; an endpoint is the natural surface and `gopsutil` is already an indirect dependency.
- **Trade-off:** one API/WS surface not spelled out in the mvp's endpoint list. No data model
  cost (live-only, no stats table per § Real-time Stats).

### D6 — Stats are subscriber-gated and never persisted; runtime logs are always-on

- **mvp.md says** (§ Real-time Stats / § Real-time Logs): both follow the same fan-out hub
  pattern; it does not specify when each producer runs.
- **We do instead:** the per-app `docker stats` collector runs **only while a WS subscriber is
  attached** (stats are live-only, no DB), whereas the `docker logs --follow` runtime-log
  followers run for every live container regardless of subscribers (logs must persist to the ring
  buffer for the Logs Explorer and crash-loop forensics).
- **Why:** running `docker stats` continuously for data nobody is watching wastes CPU; runtime
  logs must be captured unconditionally because they are persisted.
- **Trade-off:** a stats subscriber gets no backlog (none exists) and waits one poll interval for
  the first sample. Acceptable for a live gauge.

### D7 — "TLS certificate expiring" notification is deferred

- **mvp.md says** (§ Notifications): notify when a certificate expires within 14 days.
- **We do instead:** ship deploy-succeeded / deploy-failed / crash-loop / VAC-restarted in Phase 4
  and **defer** the cert-expiry event.
- **Why:** Phase 3 tracks `domains.cert_status` as advisory only (no reliable `not_after` per
  host); a correct 14-day warning needs real expiry data from a Caddy PKI read-back that Phase 3
  did not build. Shipping it now would mean a flaky notification.
- **Trade-off:** no proactive cert-expiry alert in MVP — mitigated because Caddy auto-renews. To
  revisit when cert read-back exposes per-host `not_after`; then add the event to the existing
  dispatcher (cheap once the data exists).

### D8 — Notification webhook URLs are encrypted at rest

- **mvp.md says** (§ Notifications / § Configuration): webhook URLs are configured in Settings and
  overridable via `VAC_NOTIFY_*`; it lists only `VAC_MASTER_KEY`/`VAC_ADMIN_TOKEN` as "secrets".
- **We do instead:** store the Discord/Slack webhook URLs **encrypted with `crypto.Box`** (like
  env vars / SSH keys / TOTP secrets), redact them on read, and env-only the overrides.
- **Why:** a webhook URL is a bearer secret — anyone holding it can post to the channel — so it
  belongs with the other at-rest secrets rather than as plaintext in a settings row.
- **Trade-off:** notification settings require `VAC_MASTER_KEY` to be set (same posture as TOTP
  setup); without it, storing a URL returns a clear error and only the `VAC_NOTIFY_*` env path
  works.

---

> Maintenance note: when a deviation is later reconciled (e.g. we adopt the mvp's original
> approach, or update `mvp.md` to match), mark the row **Resolved** with the date and the
> commit/PR rather than deleting it — the history is the point.
