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

### D7 — "TLS certificate expiring" notification ~~is deferred~~ (RESOLVED, plan 03)

- **mvp.md says** (§ Notifications): notify when a certificate expires within 14 days.
- **Originally deferred because** Phase 3 tracked `domains.cert_status` as advisory only, with no
  reliable per-host `not_after`; a correct warning needed real expiry data.
- **Now shipped (Track C / plan 03):** the `certcheck` package reads each managed host's real expiry
  the way a browser does — a daily TLS handshake to the proxy with the host's SNI, reading the served
  leaf certificate's `NotAfter` (`internal/certcheck`). The control plane is off `vac-edge` and
  Caddy's admin API exposes no per-host `not_after`, so the SNI-probe is the topology-friendly read.
  A new `cert_expiring` event fires through the existing dispatcher once per threshold crossing
  (`domains.cert_expiry_notified_at` de-dupes; the stamp clears on renewal). Window is
  `VAC_CERT_EXPIRY_DAYS` (default 14); the probe target is `VAC_CERT_PROBE_ADDR` (default
  `<caddy-admin-host>:443`).
- **Remaining trade-off:** because Caddy renews well before the 14-day mark, this alert in practice
  only fires when **auto-renewal has failed** — which is exactly when an operator wants to know.

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

### D10 — SMTP private-address guard is opt-out, not the webhook's hard wall

- **Context:** the notification webhook SSRF guard (D8 rationale, `netguard`) is a *hard* wall — a
  webhook URL must never reach loopback/private/link-local/CGNAT space, because a typo'd or hostile
  URL pointed at `169.254.169.254` / `vac-db` / loopback is a pure attack surface. SMTP adds a
  fourth notification channel (email) that also dials an operator-set host.
- **We do instead** (plan `email-notifications`): SMTP reuses the same `netguard.IsPrivate`
  predicate and the same TOCTOU-safe shape (resolve the host, reject if any address is private,
  dial the validated literal IP) — but as an **opt-out** gated on `VAC_NOTIFY_SMTP_ALLOW_PRIVATE`
  (default off → guarded). `net/smtp` (stdlib) doesn't ride the dispatcher's guarded `http.Client`,
  so the guard is applied directly in `notify.sendEmail` rather than via the transport dial hook.
- **Why:** unlike a webhook, a *legitimate* SMTP relay can live on the LAN — a sidecar Postfix, a
  `vac-edge`-adjacent MTA. A hard wall would block that real, intended deployment; the webhook case
  has no such legitimate need.
- **Trade-off:** an operator who sets the allow-flag can point SMTP at a private address. Accepted:
  it's their own relay, explicitly enabled, and far narrower than the typo'd-public-webhook risk
  the hard wall exists to close. The SMTP password is sealed at rest exactly like the webhook URLs
  (D8); host/port/from/recipients are operator config, stored plaintext.

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
- **We do instead:** `config.PublicIPAddr()` returns `VAC_PUBLIC_IP` verbatim when set (no network
  call). When unset it auto-detects with a local-then-external precedence: first the local
  outbound-interface IP (a UDP "dial" that opens no socket) — used directly when it is already a
  public address (the VPS fast path, no egress); when it is private/loopback/link-local/CGNAT it
  queries an external IP-echo service over HTTPS (`api.ipify.org`, then `ifconfig.me/ip`, then
  `icanhazip.com`) to learn the true public IP, falling back to the local IP if every echo fails.
  The auto-detected result is cached per process (`sync.OnceValue`). It feeds `host_ip` in the
  host-stats payload and the `GET /api/instance/dns-check` comparison (resolve a hostname
  server-side, compare to the VPS IP → `points_here`).
- **Why:** copy-pasteable A-record values and a live "is it pointed here yet?" check need the real
  address, not a placeholder — and behind NAT the local route IP is the private LAN address, so the
  external echo is what makes the DNS check correct for home/local-network operators.
- **Trade-off:** behind NAT, auto-detection makes one HTTPS GET to a third-party echo service at
  startup. Setting `VAC_PUBLIC_IP` skips it entirely; if the box is offline or every echo is
  unreachable, detection falls back to the private LAN IP and the DNS check is misleading until
  `VAC_PUBLIC_IP` is set.

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

## Track D — Managed services (backups / databases / add-ons)

All of Track D ships behind `VAC_MANAGED_SERVICES` (default off): nav entries hidden, API routes
unmounted, background goroutines (backup scheduler, lazy engine watchers) never start. Zero idle
footprint on a box that uses none — the `<200 MB` claim holds.

### D1 — S3 backup destination is hand-rolled SigV4, not `minio-go`

- **Plan said:** use a single focused client (`minio-go`) for the `s3` destination.
- **We do instead:** implement S3-compatible PUT/GET/list/delete against the stdlib with a
  hand-rolled AWS Signature V4 signer (`api/internal/backup/s3.go`). Path-style addressing +
  `UNSIGNED-PAYLOAD`; the unknown-length dump is staged to a temp file so the PUT has a
  Content-Length.
- **Why:** matches the project's documented "hand-roll to dodge deps" house style (cf. Track B's
  Prometheus exposition) and keeps the dependency surface flat — no AWS/MinIO SDK transitively
  pulled. `local` remains the zero-dep default.
- **Trade-off:** the signer is ours to maintain; it's covered by canonicalization + reference-
  vector unit tests, but not exercised against a live S3 in CI. B2/MinIO/AWS share the signed
  API surface we implement.

### D1 — Managed-DB backups exec into a container by name (service-lookup fallback)

- **Context:** D1's backup engine resolves a config's `service_name` to a container via the
  `services` table. A managed database (D2) lives in a shared engine container (`vac-db`,
  `vac-mariadb`) that is **not** an app service row.
- **We do instead:** the backup engine falls back to treating `service_name` as a literal
  container name when the service lookup returns `ErrNotFound` (`docker exec` accepts names). D2
  seeds the auto-backup with `service_name = <engine container>`.
- **Trade-off:** `backup_configs` is `UNIQUE(app_id, service_name)`, so a single app provisioning
  **two** managed DBs of the same engine (both → `vac-db`) gets one auto-backup, not two. The
  common one-DB-per-app case is fully covered; the rare case degrades gracefully (logged, not
  errored).

### D2 — Engines shipped: Postgres + MariaDB; Mongo/Redis and isolated Postgres deferred

- **Plan said:** Postgres (shared `vac-db`) plus a shared lazy daemon per engine for
  mariadb/mongo/redis, and a `VAC_MANAGED_DB_ISOLATED` opt-in for a second `vac-db-managed`.
- **We do instead:** ship **Postgres** (pool DDL on `vac-db`, attached to vac-edge) and
  **MariaDB** (shared, lazily `compose up`ed `vac-mariadb`, provisioned via `docker exec`) as the
  worked SQL pair. The recipe framework (`Engine` interface + shared compose/exec helpers) is
  generic — Mongo/Redis drop in as data. `VAC_MANAGED_DB_ISOLATED` is recognized but logs a
  warning and falls back to shared (isolated instance not yet implemented).
- **Why:** the `09` stub's own strategy gate ("build when users ask"); shipping two correct SQL
  engines beats four half-tested ones. The shared admin password for a lazy engine is derived
  from `VAC_MASTER_KEY` (stable across restarts without separate storage); MariaDB writes a root
  `~/.my.cnf` so dumps carry no password on the command line.
- **Trade-off:** rotating `VAC_MASTER_KEY` rotates a running shared engine's admin password
  (documented manual step); Mongo/Redis aren't offered in the picker yet.

### D2 — Provisioning is asynchronous; status drives the UI

- The add-DB endpoint returns `202` with a `provisioning` row and runs the engine work (which may
  cold-start a shared daemon for tens of seconds) on a detached goroutine, flipping the row to
  `ready`/`error`. The connection string is sealed up front (it's deterministic from the
  generated identity + the engine's fixed alias), so the row's `secret_enc` is populated before
  the background work runs.

### D3 — Add-on templates are embedded; the clone step branches on `source=template`

- **Plan said:** (decision #7/B) embed templates and materialize them into the work dir, skipping
  git clone.
- **We do:** `apps.source` (`git`|`template`) + `apps.template_id`. The **only** deploy-pipeline
  touch in Track D is an additive branch in the clone step: `source=template` →
  `Registry.Materialize(templateID, repoDir)` (copy embedded files) instead of `cloneOrPull`. The
  rest of build/up/health/route is unchanged; `HeadCommit` on a non-git dir returns empty and is
  skipped, so template apps record no commit. **Flag for Track A at merge.**
- **Grafana flagship — lightweight datasource deferred:** the template deploys a working Grafana
  (provisioned welcome dashboard, random admin password injected as `GF_ADMIN_PASSWORD`) that
  serves out of the box. Auto-wiring a read-only datasource to VAC's `request_metrics` (which
  needs per-install credential templating into the provisioning YAML) is deferred; the dashboard
  documents adding a managed-DB datasource by hand. The catalog mechanism (embed → install →
  deploy-as-app) is the deliverable; Grafana booting served is the acceptance.

## Domains lifecycle overhaul (plan 09) — Vercel-like domain management

### D — "Vercel-like" domains: what we adopted and what we deliberately skipped

- **Context:** plan 09 brings domains up to the Vercel bar on a single-VPS, single-operator box.
- **Adopted:** domains are added/managed in one Settings hub then *assigned* to an app/service;
  each shows a live DNS configuration check (exact record, real VPS IP, Valid/Invalid/Pending that
  auto-polls); apex + www handled as a pair with a primary + 308 redirect; reassign without a
  destructive delete-add; wildcard guidance for auto-subdomains.
- **Deliberately skipped (single-box reality — do not "add back"):** nameserver delegation (VAC
  only does A/CNAME, it is not a DNS provider); TXT ownership verification ("you control the DNS
  *and* added it in VAC" plus the `CaddyAsk` on-demand-TLS gate is sufficient proof on one box);
  multi-team / domain transfer / marketplace (one operator).

### D — Automatic subdomains are derived at reconcile time, not stored as rows (F1)

- **Before:** auto-subdomains were `type='auto'` rows in `domains`, created lazily at deploy via
  `AssignAutoDomains`. A base-domain change orphaned the old rows/routes.
- **We do instead:** auto hosts are a pure function of `(app slug, HTTP services, base domain)`,
  computed by `proxy.Manager.AutoHosts` at reconcile. They emit Caddy routes with an `@id` of
  `vac-auto-{appID}-{service}` (custom domains keep `vac-route-{domainID}`); both prefixes are
  pruned by `pruneOrphans`. The `domains` table now holds **custom domains only**. Migration 00035
  drops `type='auto'` rows; the first reconcile regenerates their routes.
- **Why:** changing the base domain becomes a no-op beyond "reconcile" — routes regenerate from the
  new base and the old ones are pruned, so **orphans are structurally impossible**. Removes the
  lazy assign-at-deploy coupling. `CaddyAsk` accepts a derived auto host via `IsAutoHost`.
- **Trade-off:** per-auto cert status loses its row — recomputed in-memory (see next). Auto hosts
  sit under the operator's own wildcard, so the stakes are low.

### D — Domain status is an in-memory projection, not a stored column (F3 §1)

- **Before:** an advisory `cert_status` column, only ever set to `error` on a route-push failure
  and never reliably to `active`.
- **We do instead:** a new always-on `internal/domainstatus` engine computes one derived status per
  host (`checking`/`awaiting_dns`/`misconfigured`/`issuing`/`active`/`error`) for **all** hosts —
  custom and derived-auto alike — as a runtime projection behind a `sync.RWMutex`, never persisted.
  Migration 00035 drops the `cert_status` column. The only durable cert state (`cert_not_after`,
  `cert_expiry_notified_at`) stays owned by the `certcheck` expiry-notification job. The SNI cert
  probe is extracted to a shared `internal/certprobe` package both consumers use.
- **Why:** DNS and "is a cert served right now" are live external facts, cheap to recompute and
  stale by nature; persisting them buys a few seconds of cold-start warmth for write amplification
  and a second source of truth. A status column also couldn't cover the now-rowless auto hosts.
- **Trade-off:** status is empty (`checking`) for a few seconds after boot until the first probe
  pass; the UI renders that as a neutral spinner, not a red badge. `error` is pushed in by the
  proxy manager on a route-push failure and cleared on the next successful push, so push-truth and
  DNS-truth never overwrite each other.

### D — Status/DNS-check resolve via a public recursive resolver, not the local stub (F3 §2)

- **Context:** `net.DefaultResolver` on a VPS goes through the local stub/systemd-resolved cache,
  which respects TTL and won't see a freshly-changed record until the old one expires — so a domain
  the operator just pointed here reads `awaiting_dns` for minutes.
- **We do instead:** both the status engine and `GET /api/instance/dns-check` use an injectable
  `*net.Resolver` dialling a public recursive resolver directly (default `1.1.1.1:53`,
  `VAC_DNS_RESOLVER` overrides; empty value falls back to the system resolver for egress-blocked
  boxes). This still honours authoritative TTL but bypasses the box's local cache.
- **Why:** so "I set the record" reflects in VAC as soon as the operator's DNS provider serves it,
  not after the local cache expires. This is purely *reading* A/CNAME with fresher cache behaviour —
  it adds no record types and does not make VAC a DNS provider.
- **Trade-off:** we still cannot beat authoritative TTL; the config card says so ("DNS changes can
  take up to your record's TTL to show here") so a slow flip reads as expected, not broken. New
  dependency: `golang.org/x/net/publicsuffix` (apex vs subdomain classification, eTLD+1-aware).

### D — Nullable domain assignment + Phase-3 redirects

- **Schema (migration 00035/00036):** `domains.app_id`/`service_name` are nullable as a
  both-or-neither pair (a `CHECK`), so a custom domain can be added and DNS-verified before being
  assigned to a service; an unassigned domain emits no route. A nullable `redirect_to` makes a
  domain emit a 308 redirect route (Caddy `static_response` with a `Location` header preserving the
  request URI) to its target instead of a reverse-proxy route — the target is the "primary" domain,
  no separate flag.
- **Why:** matches the Vercel "add now, assign later" and "apex + www, pick a primary" flows on the
  single box without a destructive delete-add (an in-place `UpdateDomain` route swap).
- **Trade-off:** an unassigned-but-pointed domain still passes `CaddyAsk`, so Caddy may pre-issue a
  cert for a host not yet serving — intended cert pre-warming, not a leak (the operator added it).

### P3.4 — Interactive container shell crosses the control-plane sandbox (gated + audited)

- **Context:** `vac-api` is deliberately off the `vac-edge` network and never reaches into user
  app containers — user code can't reach the control plane and vice-versa. The control plane does
  already `docker exec` into containers non-interactively (managed backups, Track D).
- **We do instead:** `GET /api/apps/{id}/services/{name}/exec` (WebSocket) opens an interactive
  `docker exec -i -t {container} sh` over a PTY (`dockercli.ExecInteractive`, `creack/pty`),
  rendered in xterm.js. This exposes the existing exec capability *interactively* to the operator.
- **Why:** Docker-Desktop-style "shell into a container" is the single most-requested debugging
  affordance; doing it in-product beats handing operators raw `docker exec` on the host.
- **Guardrails (this crosses a trust boundary, so all three hold):**
  - **Feature-flagged off by default** — `VAC_ENABLE_SHELL` (config `enable_shell`). The route is
    not even registered unless set; the UI hides the Shell affordance on the same flag
    (instance info → `enable_shell`). Highest blast-radius feature, so default-closed.
  - **Audit-logged** — the handler writes an `audit_log` row on session open (target `app`,
    summary "opened shell into service web", metadata `{service, container_id}`). The audit
    middleware only wraps mutating HTTP verbs, so a WS GET escapes it — the handler calls
    `InsertAuditLog` directly (mirrors the env-reveal pattern).
  - **Running-only** — a stopped/crashed service has no live `container_id`; the endpoint rejects
    with 409 before the upgrade, and the UI only renders Shell for a `running` service.
- **Trade-off:** an enabled shell is a root-capable foothold into a user container from the
  control plane — accepted because it is off by default, gated by an explicit operator confirm in
  the UI, and recorded. The PTY process is reaped on disconnect (`PtySession.Close` kills + waits
  the `docker exec` child) so a dropped socket leaves no orphan. New direct dependency:
  `github.com/creack/pty` (was already an indirect dep); UI deps `@xterm/xterm` + `@xterm/addon-fit`.

---

> Maintenance note: when a deviation is later reconciled (e.g. we adopt the mvp's original
> approach, or update `mvp.md` to match), mark the row **Resolved** with the date and the
> commit/PR rather than deleting it — the history is the point.
