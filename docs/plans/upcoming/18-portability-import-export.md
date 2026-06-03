# 18 — Portability: import on-ramp & export exit-ramp (no lock-in)

**Tier:** Trust moat · **Effort:** L (small track — see phasing) · **Status:** phases 1–2 landed (backend + UI)

> **Progress (phases 1–2, backend + UI):** the `appspec` core (`vac/v1` types, `FromApp`/`ToApp`,
> round-trip tests) and the spec on-ramp/exit-ramp are implemented:
> `api/internal/appspec/` (pure types + translation) and `api/internal/portability/`
> (store/crypto orchestration). Wired as `GET /api/apps/{id}/export` (format=spec),
> `POST /api/apps/import` (idempotent on slug), and CLI `vac-api export <slug>` /
> `vac-api apply -f`. The **UI also landed**: an Import dialog on the apps dashboard
> (`ui/src/features/apps/import-app-dialog.tsx`), an Export action on app detail
> (`ui/src/features/app-detail/settings-tab.tsx`), and the client in
> `ui/src/lib/api/portability.ts`. Decisions worth noting: services are **pre-created** on
> import so domains can bind and operator config (internal port, health path) survives; the
> spec's single `build.composePath` folds to the `compose_file` column (deploy's existing
> override→column fallback keeps it functionally identical); sensitive env values are omitted
> on export and re-pasted on import (reported via `secrets_needed`). **Not yet:** only
> `format=spec` is implemented — `export` rejects any other format with "only \"spec\" is
> available (compose/k8s land in later phases)" (`api/internal/admin/portability.go`).
> **Backups + managed databases are deliberately out of the v1 spec** — documented as a gap
> (they're stateful, not pure config; revisit as additive `vac/v1` fields). Phases 3–5 (sealed
> instance→instance, compose, k8s) remain.

## Goal

Two directions of the same capability, hinged on one portable **app spec**:

- **Import (on-ramp):** create a VAC app from a single spec file (or from a bundle exported by
  another VAC instance), so getting *into* VAC is a paste-and-go, not a click-through-the-wizard
  chore — and so moving an instance to a new box is a backup/restore, not a rebuild.
- **Export (exit-ramp):** hand the operator everything needed to run the same app *without* VAC —
  a self-contained bundle plus generated **Kubernetes manifests** and/or a **standalone compose**
  with its own edge. When someone outgrows a single box (multi-node, k8s), VAC lets them leave
  cleanly instead of trapping them.

## Why it matters (strategy)

The moat is **simplicity + UX + reliability + trust**, not feature count. This is a *trust* play,
and a sharp one: VAC's own roadmap says **"No multi-node — single operator, single box"** (see
[`README.md`](README.md) → *Deliberately NOT doing*). That's the right call for the product, but
it gives VAC a real ceiling. The honest answer to "what happens when I outgrow one box?" is the
single most reassuring thing VAC can offer a prospective user: **"you export to k8s in one click
and walk away — nothing here is proprietary."** A credible exit ramp *de-risks the on-ramp*. It is
the opposite of lock-in, and saying it out loud is itself marketing.

It also subsumes two adjacent needs almost for free: **instance migration** (move VAC from box A to
box B) and **disaster recovery** (re-create apps from a spec in version control) — both fall out of
the same import/export plumbing.

## The hinge: what's actually locked in

A VAC app is **already** mostly portable — it *is* a git repo plus a compose file. The only state
VAC adds around that compose is the lock-in surface, and it's small:

| Surface | Where it lives | Import job | Export job |
|---|---|---|---|
| **Env vars** | VAC DB, sealed with `crypto.Box` (`VAC_MASTER_KEY`) | re-seal with dest key | decrypt → `.env` / k8s `Secret` (sensitive!) |
| **Domain → service routing** | Caddy admin state, *not* the user's repo | recreate `domains` rows | translate to Ingress / published ports |
| **Resource limits** | `apps.mem_limit_mb` | set column | k8s `resources.limits.memory` / compose `mem_limit` |
| **Deploy triggers** | `deploy_triggers` rows | recreate rows | drop (target's CI/CD owns this) — note in README |
| **Deploy SSH key** | `ssh_keys`, sealed private key | re-seal *or* regenerate + reprint pubkey | export pubkey only; private key is per-instance |

Everything else an app needs (the build itself, the services, the compose) is in the user's repo
already. **That's the whole reason this is L-not-XL:** we externalize four things, we don't
reverse-engineer the app.

## The portable app spec (`vac.app.yaml`)

One declarative file, the lingua franca for both directions. Derived from the actual store models
(`apps`, `services`, `domains`, `env_vars`, `deploy_triggers`, `ssh_keys`) — operational columns
(status, container IDs, restart/OOM counts, cert expiry, timestamps) are **excluded** by design;
they're runtime state, not configuration.

```yaml
apiVersion: vac/v1
kind: App
metadata:
  name: My Blog
  slug: my-blog            # optional on import; derived from name if absent
source:
  type: git                # git | template
  url: git@github.com:me/blog.git
  branch: main
build:
  kind: compose            # auto | compose | dockerfile | framework | static
  composePath: compose.yaml
  # dockerfile: { dockerfilePath: Dockerfile }
  # framework:  { framework: react, buildCommand: "...", port: 3000, startCommand: "..." }
  # static:     { staticDir: dist, spaFallback: true }
resources:
  memLimitMB: 512          # null/omit = unlimited
services:                  # the routable surface VAC knows about
  - name: web
    internalPort: 3000
    healthPath: /healthz
deploy:
  triggers:
    - { event: push, filter: main }
    - { event: manual }
domains:
  - { hostname: blog.example.com, service: web, type: custom }
  - { hostname: www.example.com, redirectTo: blog.example.com }
env:
  - { key: NODE_ENV,     value: production,  sensitive: false }
  - { key: DATABASE_URL, sensitive: true }      # value omitted by default — see Secrets
```

`build.kind` + the matching sub-block map 1:1 to `apps.build_kind` / `apps.build_config`
(`adapter.BuildConfig`); reuse `adapter.Validate()` so import rejects an inconsistent spec the same
way the create API does.

## Secrets handling (the hard part — get this right)

Env values and the deploy private key are sealed at rest and **must never be silently dumped in
plaintext** into a file an operator might commit. Three honest options, pick per-direction:

1. **Spec-only (default):** `vac.app.yaml` lists env *keys* with `sensitive: true` but **omits
   values**. Re-paste secrets on the far side. Safe to commit; loses nothing the operator doesn't
   already have. Good default for import-from-file.
2. **Sealed bundle (instance→instance):** export ciphertext as-is, import re-seals with the
   destination `VAC_MASTER_KEY`. Requires both instances or a key-rewrap step. The right path for
   *instance migration / DR*.
3. **Plaintext export, gated:** for the **exit ramp** the operator genuinely needs the cleartext
   (k8s `Secret`, `.env`). Decrypt only on an explicit, audited action; write secrets to a
   **separate** `secrets.env` (never the YAML), mark the bundle sensitive, recommend a passphrase
   wrap, and `audit_log` the export. The deploy private key is per-instance trust material — export
   the **public** key only; for a new git host the operator rotates it.

## Part A — Import (on-ramp)

- `vac apply -f vac.app.yaml` (CLI) and/or a dashboard "Import app" paste box → one new app, no
  wizard. Idempotent on `slug`: create or update-in-place (update is the instance-migration path).
- **Compose passthrough:** if the operator just points at a repo with a compose file, that already
  works at deploy time; "import" is the thin layer that creates the app row + domains + env from a
  spec instead of clicking. Reuse the existing create/validate handlers — import is a *batch* over
  them, not a new pipeline.
- **From another VAC:** `GET /api/apps/{id}/export` → bundle; `POST /api/apps/import` ← bundle.
  Whole-instance variant iterates all apps (+ notification settings) for DR.

## Part B — Export (exit-ramp)

A `GET /api/apps/{id}/export?format=…` (+ CLI `vac export <slug>`) producing a tarball:

- **`format=spec`** — just `vac.app.yaml` (+ omitted/sealed secrets per above).
- **`format=compose`** — standalone `docker-compose.yml`: the user's resolved compose **plus** a
  generated edge (Caddy/Traefik sidecar configured from the `domains` rows, since VAC's Caddy state
  doesn't live in the repo) + a `secrets.env`. "Here's your stack, runnable on any Docker host."
- **`format=k8s`** — per HTTP service: `Deployment` + `Service` + `Ingress` (from `domains`), env →
  `ConfigMap`/`Secret`, `memLimitMB` → `resources.limits.memory`. Lean on **Kompose** as a starting
  translator, then layer VAC's routing/limits/domain knowledge on top (Kompose alone can't know the
  Caddy-alias routing — that's exactly the lock-in surface we externalize). Ship a `README.md` in
  the bundle naming the manual steps (DNS, registry, the deploy-trigger gap).

> Honesty over completeness: the generated manifests are a **correct starting point**, not a
> guaranteed turnkey cluster. Say so in the bundle README. A believable 90% export beats a
> magical-but-wrong one and protects the trust this whole feature is buying.

## Where it lives

- New `api/internal/appspec/` — the `vac/v1` types, marshal/unmarshal, `FromApp(store…) → Spec`
  and `ToApp(Spec) → store…`. Single source of truth for both directions; pairs with
  `adapter.Validate()`.
- New `api/internal/appspec/export/` (or `k8s.go`/`compose.go` generators). Keep the k8s/Kompose
  dependency isolated here so the core binary stays lean (shell out, or vendor narrowly).
- Handlers: extend `api/internal/server/handler/apps.go` with export/import routes; both mutating
  routes inherit the existing audit middleware.
- CLI: `admin` subcommands (`vac apply` / `vac export`) reuse the same `appspec` package.
- UI: an "Import" entry on the apps dashboard and an "Export / Leave VAC" action on app detail
  (the exit-ramp action doubles as a trust signal — show it, don't hide it).

## Phasing (suggested order)

1. ✅ **`appspec` core** — `vac/v1` types + `FromApp`/`ToApp` + round-trip tests. Nothing user-facing;
   unblocks everything. **Done.**
2. ✅ **Export `format=spec`** (secrets omitted) + **Import from spec** (re-paste secrets). The
   minimum honest no-lock-in story; also the DR primitive. **Done** (backend + CLI + UI).
3. ⬜ **Instance→instance** (sealed-bundle re-wrap) — the migration/DR path; whole-instance variant.
4. ⬜ **Export `format=compose`** (standalone + generated edge).
5. ⬜ **Export `format=k8s`** (Kompose + routing/limits overlay + bundle README). The headline
   exit-ramp; do last, it's the most surface area.

## Deliberately out of scope (guard the moat)

- **No per-PaaS importer arms race.** No Heroku `app.json` / CapRover / Railway / Dokku importers.
  The on-ramp is *our* spec + plain compose; everything else is an endless adapter treadmill (same
  reasoning as "no buildpack/framework-coverage arms race"). One format in, two formats out.
- **This is NOT multi-node in the product.** It generates artifacts for *other* platforms; `vac-api`
  never learns to orchestrate >1 box. Reconciles cleanly with the README non-goal — it's the exit,
  not a pivot.
- **No live/zero-downtime cutover** to the target platform. Export hands over artifacts; the
  operator runs the migration. VAC doesn't drive someone else's cluster.

## Open questions

- **Spec version & forward-compat:** commit to `apiVersion: vac/v1` now; how do we evolve it? (lean
  on additive fields + a `kind`/`apiVersion` gate.)
- **Kompose: vendor, shell-out, or hand-roll the generator?** Shelling out adds a runtime dep on a
  control box we keep deliberately minimal; hand-rolling is more code but zero dep. Probably
  hand-roll the small subset we emit.
- **Standalone-compose edge:** generate a Caddy sidecar (matches VAC's own edge, least surprising)
  vs. Traefik vs. just `ports:` + a README. Caddy keeps the mental model continuous.
- **Secrets default for the k8s/compose exit bundle:** plaintext-gated-and-audited vs.
  passphrase-wrapped by default. Lean toward wrapped-by-default with an explicit `--plaintext`.
- **Does whole-instance export include `notification_settings` and users/2FA?** Settings yes (re-seal);
  auth material almost certainly no — re-onboard on the new box.

## Acceptance (sketch)

- `vac export <slug> --format=spec` → a `vac.app.yaml` that, fed to `vac apply -f` on a fresh VAC,
  recreates the app (build config, services, domains, triggers, resource limit, env *keys*) byte-for-
  byte on the configuration columns; the operator re-enters secret values once.
- An instance-migration export from box A imports on box B with secrets intact (re-sealed under B's
  `VAC_MASTER_KEY`), no plaintext touching disk.
- `vac export <slug> --format=k8s` → a tarball whose manifests `kubectl apply` into a working
  Deployment+Service+Ingress for each HTTP service, env wired via Secret/ConfigMap, with a README
  naming the manual gaps (DNS, image registry, deploy triggers).
- Every export/import action lands an `audit_log` row; secret values never appear in any
  committable file.
