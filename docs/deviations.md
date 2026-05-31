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

### D9 — Per-key env sensitivity, but every value stays sealed at rest

- **mvp.md says** (§ Secrets / § Configuration): env vars are encrypted at rest with
  `crypto.Box` and never returned by the API — the UI renders `●●●●` for every key.
- **We do instead** (improvements plan `04-env-overhaul`): each env var carries a
  `sensitive BOOLEAN` flag (migration `00016`). **Non-sensitive** values are returned
  decrypted by `GET /apps/{id}/env` so the UI can show/edit them inline; **sensitive**
  values are still withheld and only disclosed through an explicit
  `GET /apps/{id}/env/{key}/reveal` call (audit-logged via `slog`). Crucially, **every row
  remains sealed at rest** regardless of the flag — we picked the plan's "uniform" option
  (seal everything; gate read-back on `sensitive`) over a separate `value_plain` column.
- **Why:** it preserves the "encrypted at rest" invariant verbatim — there is no plaintext
  column on disk — while still letting operators read and edit ordinary config (`NODE_ENV`,
  `PORT`) the way Vercel does. The flag is purely a read-back policy, not an at-rest one.
- **Trade-off:** the list endpoint now decrypts non-sensitive values on read, so it needs
  `VAC_MASTER_KEY` to be set (returns 503 otherwise) — previously list worked key-less since
  it returned no values. Full-replace `PUT` semantics are unchanged; the UI resolves any
  unrevealed sensitive values (via reveal) before submitting so a save never drops a secret.

---

## Improvements batch (2026-05-31) — settings/instance

### D — Runtime-editable base domain lives in a DB singleton, not just config

- **Context:** auto-subdomains need a base domain, previously config-only (`VAC_BASE_DOMAIN`),
  set at boot and read by `proxy.Manager`.
- **We do instead:** a singleton `instance_settings` row (migration `00018`) holds a
  runtime-editable `base_domain`. The Domains settings tab reads/writes it via
  `GET/PUT /api/instance/base-domain`. On write, the handler persists to the DB **and** calls
  `proxy.Manager.SetBaseDomain` (a mutex-guarded override) so new auto-domains use it
  immediately; on boot, `main` loads the row into the manager. Empty falls back to config.
- **Why:** operators expected to set the base domain from the dashboard without redeploying the
  control plane. The override is additive — when unset, behaviour is identical to before.
- **Trade-off:** the effective base domain now has two sources (DB row wins over config). The
  manager reads it through `baseDomain()` rather than `cfg.BaseDomain` directly.

### D — Instance control-plane restart is a self-exit relying on the container restart policy

- **Context:** the Danger-zone "Restart control plane" must restart vac-api + vac-proxy while
  leaving app containers on `vac-edge` running. vac-api cannot cleanly `docker restart` itself
  from inside the dying container.
- **We do instead:** `POST /api/instance/restart-control-plane` restarts `vac-proxy` synchronously
  via `docker restart` (raw container, `dockercli.RestartContainers`), responds `202`, then sends
  itself `SIGTERM` after a short delay. Graceful shutdown runs and the container's
  `restart: unless-stopped` policy brings vac-api (and the in-process worker) back. The UI shows a
  "reconnecting…" state and reloads once the API answers again.
- **Why:** a child `docker restart vac-api` spawned from within vac-api is racy (the child can die
  with its parent). Leaning on the restart policy is deterministic and needs no host agent.
- **Trade-off:** requires the deployment to set a restart policy on `vac-api` (the prod compose
  does). If it doesn't, vac-api stays down after the self-exit. App containers are untouched.

### D — Instance reset wipes app stacks + rows but preserves the control-plane schema

- **Context:** Danger-zone "Reset instance" must wipe apps/deployments/databases behind a typed
  confirmation.
- **We do instead:** `POST /api/instance/reset` requires the body to echo `RESET` (re-validated
  server-side, rejected `400` otherwise), then for each app runs `docker compose down -v`
  (containers + volumes) and `DeleteApp` (cascades deployments/services/domains/env). The control
  plane, its users/sessions, and the schema survive. Best-effort per app so one stuck stack can't
  block the wipe; counts of removed/failed are returned.
- **Why:** "reset" means start clean on apps, not re-bootstrap the operator account. Typed
  confirmation on both client and server guards an irreversible action.
- **Trade-off:** orphaned images aren't pruned here (the existing image-retention pruner handles
  that); a failed `down` still deletes the row, so a wedged stack may need manual `docker` cleanup.

### D — VPS public IP is surfaced via host stats and the DNS-check endpoint

- **Context:** the sidebar host row and the per-domain DNS guidance need the VPS's public IP.
- **We do instead:** `config.PublicIPAddr()` returns `VAC_PUBLIC_IP` when set, else best-effort
  outbound-interface detection (a UDP "dial" that opens no socket). It feeds `host_ip` in the
  host-stats payload and the `GET /api/instance/dns-check` comparison (resolve a hostname
  server-side, compare to the VPS IP → `points_here`).
- **Why:** copy-pasteable A-record values and a live "is it pointed here yet?" check need the real
  address, not a placeholder.
- **Trade-off:** auto-detection returns the primary route's source IP, which on a NAT'd host may
  differ from the true public IP — operators set `VAC_PUBLIC_IP` to override.

### D — Build adapters resolve to a compose file; `compose_file` kept for back-compat

- **Context:** plan 03 adds build adapters (compose / dockerfile / framework / static) so users
  can deploy more than a hand-written compose file. Schema gains `apps.build_kind` (default
  `auto`) and `apps.build_config` (JSONB).
- **We do instead:** every adapter ultimately *produces a compose file* the existing pipeline
  builds & ups — the deploy path stays compose-driven, so the vac-edge routing and Caddy
  health-gating invariants (D2/D3) hold unchanged. The legacy `compose_file` column is **kept**
  rather than folded into `build_config.composePath`: when the compose adapter's `composePath`
  is empty, the pipeline falls back to `compose_file`, so pre-adapter apps deploy untouched.
  Generated adapters (dockerfile/static/framework) write a `compose.yaml` (plus an nginx conf or
  `Dockerfile.vac`) into the repo working tree, regenerated every deploy.
- **Why:** preserving `compose_file` avoids a data migration and keeps plan 02's `DetectAt`
  detection/override behaviour working as the compose adapter's core.
- **Trade-off:** two places nominally describe "where the compose file is" (the column and
  `build_config.composePath`); the column is authoritative only when `build_kind` is `auto` or
  `compose`. Static/framework routing relies on the generated service publishing a port so VAC
  auto-detects its internal port (the same mechanism as a user compose with `ports:`).

---

> Maintenance note: when a deviation is later reconciled (e.g. we adopt the mvp's original
> approach, or update `mvp.md` to match), mark the row **Resolved** with the date and the
> commit/PR rather than deleting it — the history is the point.
