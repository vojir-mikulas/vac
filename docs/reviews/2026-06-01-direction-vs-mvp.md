# State review — MVP & strategic direction (2026-06-01)

A point-in-time read of where the build sits against two yardsticks:

1. `docs/plans/mvp.md` — the original feature contract.
2. The **Strategic Direction** doc (Notino) — the "why" and the guardrails.

Verified against source at commit `e6524ec`. This is an opinion piece, not a generated KB
file — treat it as a snapshot of judgement, not current truth.

---

## TL;DR

- **Vs. MVP: ~95% there.** Every one of the six build phases is present in code. The gaps
  are small and deliberate (documented in `docs/deviations.md`): cert-expiry notification,
  per-app history charts, and the multi-step onboarding wizard. None are load-bearing.
- **Vs. Strategy: strongly aligned, one drift to watch.** Single-node, Compose-driven,
  modular monolith, no Kubernetes/multi-cloud/microservice contamination, low-RAM posture
  intact. The one place reality is *ahead* of the strategy's "early stage" advice is **build
  adapters** (framework/static/Dockerfile auto-generation) — a DX win that's worth keeping
  on a leash.

---

## Part 1 — How far off `mvp.md`?

### Done and matching the contract

| Area | Status |
|---|---|
| Git connect via SSH deploy keys (+ HTTPS public repos), test-connection | ✅ `gitcli`, `sshkey`, `test_connection.go` |
| Compose primary model + single-Dockerfile auto-wrap | ✅ `compose/detect.go`, `compose/wrap.go` |
| Stack lifecycle (start/stop/restart, per-service restart) | ✅ `stack_control.go` |
| Rich service status model + crash-loop detection/stop | ✅ `deploy/status.go`, `crashloop/` |
| Automatic HTTPS via Caddy, auto-subdomains, custom domains | ✅ `caddy/`, `proxy/`, `domains.go` |
| Env vars encrypted at rest + `.env` paste import | ✅ `env.go`, `crypto.Box` |
| Live build + runtime logs, per-service stats, host stats | ✅ `ws/`, `logstream/`, `stats/` |
| Discord/Slack notifications | ✅ `notify/` |
| Auth: password + TOTP + sessions + API tokens + CSRF + rate limit | ✅ `auth/`, handlers present |
| Dark mode, embedded UI via `go:embed` | ✅ |
| Graceful shutdown, `/health`, log retention pruning | ✅ `health.go`, `retention/` |

The full API surface in `mvp.md` is implemented, and the handler list has actually grown
past it (instance management, host stats, caddy-ask, metrics).

### Gaps / deferred (all intentional, all in `deviations.md`)

- **D7 — Cert-expiry notification not shipped.** Needs reliable per-host `not_after` that
  Phase 3 didn't build. Mitigated by Caddy auto-renew. *Lowest-effort MVP-completeness item
  left.*
- **Per-app CPU/RAM history charts** — explicitly deferred (improvements README). Live gauges
  exist; historical line charts with 1h/6h/24h selector from the mock do not.
- **Multi-step onboarding wizard** — `mvp.md` specs a 4-step wizard (account → instance → SSH
  key → first deploy). Only the admin-account setup (`routes/setup.tsx`) exists; the guided
  remainder is deferred.
- **D1 — Request metrics from access log, not Prometheus `/metrics`.** This is a *correction*,
  not a shortfall — the mvp's stated approach was technically impossible (Caddy's Prometheus
  labels can't attribute a request to an app/service).
- **D4 — Per-host on-demand TLS instead of wildcard-by-default.** Wildcard is now opt-in. End
  users can't tell the difference.

### Verdict on MVP

The success-criteria checklist in `mvp.md` is essentially satisfiable today. What remains is
polish and two genuinely-deferred features (history charts, guided onboarding) plus one cheap
loose end (cert-expiry alert). I would call the MVP **functionally complete**.

---

## Part 2 — How far off the Strategic Direction?

### Where we're dead-on

- **Single-node, single-VPS.** No clustering, no scheduler, no node abstraction anywhere.
- **Compose-driven.** Even the new build adapters *compile down to a compose file* the
  existing pipeline builds — the deployment model never forks (`deviations.md` D-adapters).
- **Modular monolith.** One Go binary is both API and deploy worker; packages are
  single-responsibility under `api/internal/`. No auth-service/deploy-service split. This is
  exactly the "prefer modular monolith over microservices" instruction, honoured literally.
- **No Kubernetes mimicry.** No reconciliation controllers, no desired-state loops, no CRDs.
  The closest thing — the boot-time `vac-edge` re-attach and the in-progress→interrupted
  sweep — are *one-shot startup reconciles*, not continuous controllers. Good restraint.
- **No multi-cloud abstraction.** Pure Linux + Docker socket. No Terraform-like layer.
- **Low operational overhead by design.** Caddy owns HTTPS/health; the operator manages no
  reverse proxy, certs, or networking by hand. This is the "remove operational pain" thesis
  expressed in architecture.
- **"Bring your own stateful services."** The Database page literally tells the user VAC runs
  one shared Postgres for its own state and that *managed databases are "planned for a future
  release"* — precisely the strategy's "do NOT initially build managed databases" stance, with
  the monetization hook (Managed VAC) left as a future seam.

### The one drift to watch: build adapters

The strategy's early-stage guidance is blunt: *"Prefer user-provided Docker Compose… Do NOT
initially build orchestration layers or advanced provisioning systems… validates real demand."*

We've shipped **framework + static + Dockerfile adapters** (`api/internal/adapter/`) that
auto-detect a repo's type and *generate* compose/nginx/Dockerfile for the user. This is past
the "user hands us a compose file" floor.

This is **not** a strategic violation — it serves the headline promise ("deploy full-stack apps
in minutes", "minimal friction", "fast onboarding") and it was carefully constrained so the
deploy path stays compose-only. But it's the category of feature the strategy warns *grows*:
every framework you claim to detect is a maintenance surface and a support expectation.

**Recommendation:** keep the adapter set small and honest. Detect-and-generate for the obvious
cases (static site, lone Dockerfile, one or two dominant frameworks), and fail *loudly to a
"write a compose file" path* for everything else rather than chasing buildpack-style coverage.
The moment adapters start accreting per-framework special-casing, that's complexity drift away
from the simplicity moat.

### Minor tension points (none serious)

- **Control-plane HTTPS, instance reset/restart, DNS-check, mock-preview, a11y plans** are all
  scope *additions* beyond `mvp.md`. Every one of them reduces operational pain or improves DX,
  so they're strategy-positive — but they're also why the surface is bigger than the original
  MVP. Worth being conscious that the backlog (`docs/plans/improvements/`) is steadily
  broadening the product. That's fine *if* each item keeps earning its keep against "least
  painful deployment experience."
- **RAM target (<200 MB idle).** The strategy and MVP both lean on this as a differentiator. I
  did not measure it in this review — it's the one criterion I'd actually *benchmark* before
  claiming the MVP is shippable, because every subsystem added since (stats collectors, log
  followers, req-metrics tailer, ws hub) nibbles at it.

---

## What I'd do next (priorities, strategy-weighted)

1. **Benchmark idle RAM** on a real 2 GB VPS with a few apps deployed. It's the headline claim
   and the only success criterion not verifiable by reading code. (highest value, low effort)
2. **Ship the cert-expiry notification** (D7) — cheapest remaining MVP-completeness item once
   per-host `not_after` read-back exists.
3. **Hold the line on build adapters.** Decide explicitly which frameworks are in scope and
   make "unsupported → write a compose file" a clean, documented fallback. Resist buildpack creep.
4. **Decide onboarding-wizard fate.** Either build the guided 4-step flow or update `mvp.md` to
   match the leaner reality, so the contract and the code stop disagreeing.
5. **Per-app history charts** when there's appetite — purely a UX nicety, not blocking.

---

*Author's note: source was trusted over docs throughout. If this review and a KB file disagree,
the code wins and the KB file is stale — see `CLAUDE.md`.*
