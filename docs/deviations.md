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

> Maintenance note: when a deviation is later reconciled (e.g. we adopt the mvp's original
> approach, or update `mvp.md` to match), mark the row **Resolved** with the date and the
> commit/PR rather than deleting it — the history is the point.
