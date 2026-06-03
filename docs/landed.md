# Landed

A short, append-only log of what's merged to `main`. One entry per feature/fix —
newest at the top. Keep each entry to a few lines: what changed, why it matters,
and a pointer (commit, plan, or KB file) for the detail. This is a human-readable
changelog, not the source of truth — code and `docs/kb/` win.

Format:

```
## YYYY-MM-DD — short title
One or two sentences on what landed and why. (commit `abcdef0`, plan/KB link)
```

---

## 2026-06-03 — Deploy queue: concurrency, cancellation, live panel

Deploys now run through an operator-tunable pool instead of a single hard-coded
worker. A new `max_concurrent_deploys` instance setting (default 1, capped at 8;
Settings → Deployments) sizes the worker pool at boot, so a burst of pushes
across many apps no longer all rebuild at once on a small VPS. A
`one_active_deploy_per_app` partial-unique index makes coalescing atomic across
**every** trigger path (manual/rollback/webhook close the prior check-then-insert
race) and guarantees two pool workers never race one app's git workdir + compose
stack. Deploys are cancellable (queued or in-flight) via a new `canceled`
terminal status and a per-deploy context registry in the worker — cancelling
aborts the build/up subprocess but never tears down the running stack. A new
deploy-queue side panel (topbar, with a live count badge) shows running + queued
deploys across all apps over a `deployments` WS topic, each row cancellable.
(plan `docs/plans/upcoming/20-deploy-queue-concurrency.md`; migration `00062`)

## 2026-06-03 — P4 domains (P4.1/P4.2)

The base-domain card no longer reads as empty when the domain comes from
`VAC_BASE_DOMAIN`/`vac.yaml`: a computed `config.BaseDomainSource` plus a
`source` field on GET/PUT `instance/base-domain` drive a "Currently effective:
`host` — from <env/file/override>" line, the effective value as placeholder
(not pre-filled, so inheriting never silently becomes a pinned override), and
the wildcard tip now gates on the effective value (P4.1, the confirmed display
bug — no router/precedence bug existed). Per-app custom domains are now
manageable from an app's **own** Settings tab via a new `AppDomainsSection`
(reuses `DomainStatusBadge` + `DomainConfigPanel`): lists custom + read-only
auto hosts, adds against a service, deletes custom ones — for git and add-on
apps alike (P4.2, pure surfacing of existing endpoints). No migration. (commits
`cd90fd3`, `25d63c1`; plan `docs/plans/triage/P4-domains.md`)

## 2026-06-03 — P5 security honesty (P5.1/P5.3/P5.4)

The Security dashboard now says *why* a panel is empty instead of reading as
"everything's failing." fail2ban/firewall carry a typed `status`
(`healthy`/`unreadable`/`not_installed`/`disabled`) — from inside the sandboxed
`vac-api` container the host binaries are structurally absent, so reads report
`unreadable` with copy pointing at the (future) read helper, never a misleading
"not detected" (P5.1). The traffic panel distinguishes a disabled monitor, an
unreadable/non-JSON Caddy access log, and a genuinely idle box via a new
`reqmetrics.Collector.Stats()` + `monitoring`/`log_readable`/`parsed_lines` on
the response (P5.3). The Security nav item shows a red count badge of failing
(`error`) posture checks, derived client-side from the cached posture query
(P5.4). P5.2 (opt-in privileged host read helper) was **graduated** to its own
stub rather than built. (plan `docs/plans/triage/P5-security-and-metrics.md`;
graduated `docs/plans/upcoming/20-host-read-helper.md`)

## 2026-06-03 — P3 app-detail UX

App-detail got four upgrades: stack start/stop/restart-all lifted into the
header (reachable from every tab); per-service **Stop**/**Start** + **View logs**
(routes to the Logs tab pre-filtered to that service); a right-column **overview
panel** (Source: repo/branch/commit/framework or add-on; Stack: build kind,
service count, RAM cap, network); and an interactive **container shell** —
`docker exec -it … sh` over a WebSocket PTY in xterm.js. The shell is privileged
(control plane → user container), so it's `VAC_ENABLE_SHELL`-gated (off by
default), confirm-gated in the UI, running-only, and audit-logged per session.
Wired into the installer: a guided-setup question, the generated `.env`, and a
`vac container-shell on|off` command. (commits `6bdf6b8`, `604e4be`, `efc622c`,
`2069003`, `6836baa`, `a6125f8`; plan `docs/plans/triage/p3-app-detail-ux-plan.md`;
`docs/deviations.md`)

## 2026-06-03 — Opt-in write-only env secrets

Env vars can now be marked **write-only**: still sealed at rest, but never
returned (reveal → 403) and non-downgradable — set/replace or delete only. An
untouched write-only secret survives the full-replace save via a `keep` path
that reuses the prior sealed bytes without decrypting, and the flag round-trips
through audit revert with no plaintext in `audit_log`. Default behaviour is
unchanged (the flag is opt-in). UI: a confirmed "Make write-only" action plus a
non-revealable row state in the env editor. (commits `a6c956a`, `553369e`;
plan `docs/plans/triage/P6-env-vars.md`; migration `00061`)
