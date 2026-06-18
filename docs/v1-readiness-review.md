<!-- code review of commit 501d830 on 2026-06-18 — point-in-time snapshot, not current-state truth; re-run before relying on it -->

# VAC v1-Readiness Code Review

A point-in-time review of the codebase against a v1 release bar, conducted on 2026-06-18
(HEAD `501d830`). Five focused passes: security, deploy-pipeline correctness/concurrency,
test & release readiness, operational readiness, and frontend. This is a **snapshot** — like
`docs/plans/`, it records intent at a moment, not current behavior. Trust the source code
over this file if they disagree.

## Bottom line

The codebase is mature, not a prototype. The hard parts are done right: secret sealing,
TOTP anti-replay, the SSRF guard, argv-based Docker invocation, the "never tear down a
running stack" invariant, footprint discipline, boot reconcile, and a clean frontend with no
XSS/auth holes. No reviewer found a Critical or High *exploitable* bug.

What stands between this and a confident v1 is a short punch list — mostly auth-consistency
one-liners, two real bugs, and one process gap (no CI). None are architectural. Estimated a
day or two of work.

## Must-fix before tagging v1

### 1. No CI gate — releases ship unverified
`.github/workflows/release.yml` builds and pushes GHCR images on `v*` tags with **no**
test/lint/typecheck job in front of it. `make lint typecheck test` all pass locally and run
`-race`, but nothing enforces that before a tag builds — a broken commit can ship. Add a
push/PR workflow running `make lint typecheck test`. *Highest-value fix.*

### 2. `netguard` has zero tests
`api/internal/netguard/netguard.go` is the entire SSRF defense for outbound
webhook/DNS/S3/SMTP calls — the one thing between a hostile URL and `169.254.169.254` /
`vac-db` / loopback. The logic reads correct (rejects private/loopback/link-local/CGNAT,
dials the validated literal IP to close DNS-rebinding), but it needs a test table. Shipping a
PaaS with the SSRF dialer untested is a real risk.

### 3. Missing step-up 2FA on two destructive instance ops
`POST /api/instance/restart-control-plane` and `POST /api/instance/stop-all-apps`
(`api/internal/server/server.go:289-290`) require only a valid session, while every
comparable op (`/reset`, `DeleteApp`, restore, cert ops, `RemoveDatabase`) is wrapped in
`RequireStepUp`. Looks accidental. A stolen/CSRF'd session can cause a full outage without
re-auth. One-line fix each: add `.With(middleware.RequireStepUp)`.

### 4. Deploy lane can strand for ~30 min on `Enqueue` failure
In `api/internal/server/handler/approvals.go:43-58` (ApproveDeployment) and
`api/internal/deploy/window_sweeper.go:87-95` (WindowSweeper.sweep), the deployment row flips
to `queued` *before* `worker.Enqueue`. If the queue is full (default 32) or errors, the row
is left `queued` — which counts in the `one_active_deploy_per_app` partial unique index
(migration 00062) — so the app is blocked from any new deploy until the reaper settles it (up
to `defaultReapTimeout` = 30 min from `triggered_at`). Roll the row back to
`pending-approval`/`scheduled` (or settle it `error`) if `Enqueue` fails.

### 5. `job_runs` is never pruned
The retention `Pruner` (`api/internal/retention/pruner.go:129-191`) covers runtime_logs,
request_metrics, audit_log, security_events, deployments, images, and build cache — but not
user-cron history. `api/internal/jobs/engine.go:25-26`'s comment implies pruning was intended
but never wired (no `DeleteJobRunsOlderThan`, no per-job row cap). Output is capped at 16 KiB
per row, so this is slow disk-fill (a per-minute job ≈ 8 GB/year), not RAM — on a box with no
headroom and no operator visibility until disk fills. Add `DeleteJobRunsOlderThan` to the
pruner, or cap rows per job on insert.

## Should fix soon (not blocking)

- **Per-IP rate limit & audit IPs collapse to the proxy IP** behind the bundled Caddy
  (`api/internal/server/middleware/ratelimit.go:113-119`). No code reads `X-Forwarded-For`,
  so the login limiter is one global bucket for the whole internet and every audit source-IP
  is the proxy's. Per-account lockout (10 failures / 15 min, atomic) still bounds brute force,
  so this is defense-in-depth degradation — but wire a trusted `X-Forwarded-For` for real
  per-IP throttling and useful audit trails.
- **No `recover` on the ~12 long-lived goroutines** (`api/main.go:301-480`). A panic silently
  kills a subsystem while `/health` still returns 200 (it checks DB/Docker/Caddy, not
  goroutine liveness). The docker event bus is the exception (backoff/retry). Add a
  recover+relaunch wrapper, or at minimum log+alert on goroutine exit.
- **No manager-level mutex on Caddy route sync** (`api/internal/proxy/manager.go`).
  `caddy/client.go` `PutRoute` is a non-atomic delete-then-append, and `Sync(appID)` has many
  concurrent callers (domain edit `handler/domains.go:453`, wake `scaletozero/waker.go:188`,
  stack-control `handler/stack_control.go:35`, the deploy pipeline) not covered by the per-app
  deploy uniqueness guard. The `m.mu` documented as the "base-domain mutex" only guards
  `baseDomainOverride`, not route pushes. Low-probability on a single-operator box, but a
  genuine race; a per-app keyed lock closes it.
- **Thin unit coverage on critical packages**: `auth` 17%, `deploy` 31% statement coverage,
  `server/handler` 11%. Backstopped by integration tests (24 `integration`-tagged files) —
  but those aren't run in CI (see #1). Quality debt for post-launch.
- **Shell exec not step-up gated** (`server.go:514`, `ExecWS`) and **free-form backup command
  not gated/validated** (`handler/backups.go:168`, runs via `sh -c` inside the app container).
  Both are authenticated-operator capabilities and the shell is off by default
  (`VAC_ENABLE_SHELL`), so lower stakes — but worth tightening for consistency
  (create/update backup are not step-up gated, so a stale session can plant a scheduled
  in-container command).
- **Cancel-vs-pipeline terminal-status race** (cosmetic): `handler/deployments.go:178-184`
  can re-label a canceled deploy as `error` in a narrow window. Always terminal, never tears
  down the stack — purely label-correctness. Add `AND status NOT IN (terminal…)` to
  `MarkDeploymentFinished`.
- **No pre-migration DB backup step** and a few intentionally destructive forward-only
  migrations (`00014`, `00035`, `00005`). Low risk (already applied on existing installs), but
  worth a note in operator docs; downgrade is unsafe (goose is forward-only, fails closed).
- **No CHANGELOG** — nice-to-have.

## Fine as-is / accepted

- **Frontend is v1-ready.** Clean typecheck, 0 lint errors (11 benign warnings), 50/50 tests
  pass. Sound 401→login redirect + step-up replay, CSRF double-submit, sandboxed
  maintenance-HTML preview iframe (`sandbox=""`), no token in localStorage (cookie-based
  session), no XSS surface (log lines render as escaped React text nodes; the only
  `dangerouslySetInnerHTML` is recharts CSS with controlled keys). English-only i18n is
  acceptable for v1 — the framework is multi-locale-ready.
- **The `<200 MB` idle claim holds** and is verifiable: every optional subsystem is gated to
  zero cost when unused (Track D behind `VAC_MANAGED_SERVICES`; backup/jobs/idle-suspend
  schedulers start only when ≥1 config exists; stats collectors subscriber-gated). All
  in-memory caches are bounded; the security monitor (the only attacker-controlled-cardinality
  map) is hard-capped at 1024 IPs with LRU eviction.
- **Boot reconcile self-heals** routing/certs/logs on restart; API boots even when
  Docker/Caddy are unreachable (non-fatal probes); user containers survive via Docker
  `restart` policy.
- **Install/upgrade flow, prod compose hardening, and embedded auto-migrations are
  production-safe.** `compose.prod.yaml` has required-secret guards, `mem_limit` +
  `GOMEMLIMIT`/`GOGC`, healthchecks, loopback-only DB. `scripts/install.sh` generates secrets
  under `umask 077`, preserves them across upgrades, and is idempotent.
- **Crypto, webhook, security, middleware** are well-tested (85.7% / 93.1% / 83.2% / 71.1%).
  Session/token hashing, TOTP replay/lockout, step-up freshness, and the SSRF guard's *logic*
  are all implemented correctly and defensively.

## Verdict by dimension

| Dimension | Verdict |
|---|---|
| Security | Strong. No exploitable Critical/High. Fix M1 (`X-Forwarded-For`) + step-up gaps. |
| Deploy correctness | Core safety invariant solid. Fix the `Enqueue` strand (#4) and add route-sync lock. |
| Tests & release | Product ready; process gaps block — add CI gate (#1) and `netguard` tests (#2). |
| Operational | Mature for single-operator. Fix step-up gap (#3) and `job_runs` pruning (#5). |
| Frontend | v1-ready. Remaining items are polish. |
