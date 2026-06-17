# Deploy from a Prebuilt Image — design sketch

Let an operator point VAC at a published image (`ghcr.io/me/app:1.4.2`) and deploy it with
**no build step and no git repo** — VAC pulls it, attaches it to `vac-edge`, routes it through
Caddy, and supervises it exactly like any other app. A fifth build adapter (`image`) alongside
`compose` / `dockerfile` / `framework` / `static`.

Status: **planned** (not started).

## Why

Today every app is git→build via an adapter (`adapter.For` dispatches on `build_kind`,
`api/internal/adapter/adapter.go:82`). That covers source you control. It does **not** cover the
common case of "I already have a CI pipeline that pushes an image; just run it." The framework
already proved the pattern that makes this cheap: the Grafana add-on is a template app that
**skips the git clone** and records no commit (`pipeline.go:206`, deviations D3). An image app is
the same shape — no clone, no commit — but the source is a registry ref instead of embedded files,
and there's a real new concern templates didn't have: **private-registry auth**.

This is one adapter + one source mode + one auth-storage decision. The deploy path stays
compose-driven, so vac-edge routing and Caddy health-gating hold unchanged.

## Key technical realities (read before building)

- **Every adapter produces a compose file; the pipeline builds & ups it.** The `image` adapter
  must too (`adapter.go:71` interface, deviations "Build adapters resolve to a compose file"). It
  generates a minimal `compose.yaml` with `image:` and **no `build:` context**. `docker compose
  build` (`dockercli/compose.go:37`) on a service with no `build:` is a no-op; `up`
  (`compose.go:53`) pulls the image if absent. So the existing Build→Up steps already do the right
  thing — no new docker verb needed for the public-image case.
- **Routing needs an internal port, and `ps` won't report one.** HTTP services don't publish host
  ports (invariant D2), so `docker compose ps` reports `TargetPort 0`; the pipeline already falls
  back to the compose `expose:`/`ports:` target via `compose.ServiceExposedPorts`
  (`pipeline.go:515`, `upsertServices`). The generated compose **must `expose:` the operator's
  declared port** so VAC auto-detects `internal_port` and attaches the alias `{slug}--app`. This
  is the static adapter's exact trick (`adapter/static.go:23` publishes `"80"`).
- **There is no `docker login` / registry-auth anywhere today.** `dockercli` wraps only
  build/up/ps/config/stop/start (`dockercli/compose.go`); compose currently relies on the host's
  ambient docker credentials. A **private** image needs explicit auth, and we should not depend on
  an operator hand-editing `~/.docker/config.json` on the box. This is the one genuinely new
  mechanism (see Phase 2).
- **No git means no commit and no clone.** `HeadCommit` on a non-git dir returns empty and is
  skipped (`pipeline.go:234`); the row simply records no commit, like template apps. Redeploy is
  triggered the same way as everything else (`handler/deployments.go` `TriggerDeployment`) — there
  is no "git push" event, so "deploy" for an image app means "pull the ref again."
- **`git_url` is currently required at create** (`handler/apps.go:177`, regex-validated). An image
  app has no git URL — the create handler must branch on the image source the way the store's
  `CreateTemplateApp` already writes empty `git_url`/`git_branch` (`store/apps.go:77`).

## What already exists (don't rebuild)

- **Adapter dispatch + interface**: `adapter.For(kind, repoDir)` switch (`adapter/adapter.go:82`)
  and the `Adapter`/`Prepare` contract (`adapter.go:71`). Add one `case KindImage`.
- **Build-config plumbing**: `apps.build_kind` (default `auto`) + `apps.build_config` JSONB
  (migration `00017_apps_build_kind.sql`); `BuildConfig` struct + `ParseConfig`/`Validate`
  (`adapter.go:39`, `:59`, `:119`); handler `normalizeBuildConfig` validates and canonicalizes
  (`handler/apps.go:143`). New `image` fields slot into the same struct.
- **The "skip clone" branch**: the template branch in the clone step (`pipeline.go:206`) is the
  exact seam — add a sibling branch for the image source that creates the work dir without cloning.
- **Empty-commit tolerance**: `pipeline.go:234` already ignores an empty `HeadCommit`.
- **Port auto-detect for `expose`-only services**: `compose.ServiceExposedPorts` +
  `upsertServices` (`pipeline.go:510`); the generated compose just needs `expose:`.
- **Generated-compose precedent**: the static adapter writes `compose.yaml` into the work dir from
  a template and returns its path (`adapter/static.go:35`); `compose.Wrap` is the same idea
  (`compose/wrap.go:41`). `composeWrapPath` (`adapter.go:166`).
- **Secrets at rest via `crypto.Box`**: `Seal`/`Open` (`crypto/aead.go:43`), used for SSH keys
  (`sshkey/manager.go:46`/`:70`), env vars, TOTP, webhook URLs. Registry creds follow the same
  pattern — sealed bytes in a column, never returned by the API.
- **Env injection**: `RenderEnvFile` writes the sealed env vars to `.env`
  (`pipeline.go:336`); the generated compose includes `env_file: - .env` (like
  `adapter/framework.go:41`), so env injection works unchanged.
- **Preflight still runs**: `compose.PreflightBytes` over the resolved config (`pipeline.go:292`)
  lints the generated file — a pulled image declaring `privileged`/`host` net or mounting
  `docker.sock` is impossible here (we author the compose), but the guard runs regardless.
- **Build-kind picker UI**: `BuildSourcePicker` with `KIND_OPTIONS` + per-kind fields
  (`ui/src/features/apps/build-source.tsx:22`), driven from `new-app.tsx`. Add an `image` card.
- **Image-retention pruner**: nightly keeps N images/service (`retention/pruner.go`) — image apps
  benefit from this for free (old pulled tags get pruned).

## Scope decisions (the important part)

1. **New source mode `image`, not just a build_kind.** `apps.source` is already `git|template`
   (`store/apps.go:19`, migration `00042`). Add `image` to that enum: it's what gates the
   clone-skip branch and the "no git_url" create path. `build_kind` is set to `image` in lockstep
   (one image app = `source=image`, `build_kind=image`). Keeping both honest mirrors how template
   apps are `source=template`, `build_kind=compose`.
2. **`build_config` carries the image ref + port; credentials live in their own sealed column.**
   The image ref and internal port are plain config (`build_config`); a registry **password/token
   is a bearer secret** and belongs sealed at rest like env vars — not in a plaintext JSONB blob.
   See Phase 2 for the column.
3. **Single service named `app`.** An image deploy is one container, full stop. Multi-service =
   write a compose file (the compose adapter). This keeps the generated file trivial and the alias
   deterministic (`{slug}--app`).
4. **Redeploy = re-pull the configured ref.** No "check for new image" polling in v1. Pressing
   Deploy runs `docker compose pull` (new `dockercli` verb) then up, so a moved tag (`:latest`,
   `:stable`) is picked up. A digest-pinned ref (`@sha256:…`) is immutable and pulls once. (A
   later "watch for new digest" poller is out of scope — see below.)
5. **Public images need zero new auth.** The credentials path (Phase 2) is opt-in; a public
   `ghcr.io`/Docker Hub image deploys with Phase 1 alone.

## Phase 1 — The `image` adapter (public images)

- **Adapter**: `api/internal/adapter/image.go`, `imageAdapter{}` with `Kind() == KindImage`.
  `Prepare` validates the ref (non-empty, parses as `[registry/]repo[:tag|@digest]`), then writes
  a generated `compose.yaml` to `composeWrapPath(repoDir)`:
  ```yaml
  # Auto-generated by VAC for a prebuilt image. Do not edit — regenerated every deploy.
  services:
    app:
      image: ghcr.io/me/app:1.4.2
      restart: always
      expose:
        - "8080"          # cfg.Port → vac-edge auto-detect; omitted when Port==0 (worker)
      env_file:
        - .env
  ```
  No `build:`, no `ports:` (no host publish — invariant D2). `expose` only when `Port > 0`; a
  port-less image is treated as a non-HTTP worker (no route, like any port-less service).
- **BuildConfig fields** (`adapter.go:39`): add `Image string json:"image"` and reuse the existing
  `Port int` (already in the struct for framework). `Validate` for `KindImage`: `Image` required,
  `Port` in `0..65535`.
- **Pipeline clone-skip branch** (`pipeline.go:206`): add `else if app.Source ==
  store.AppSourceImage { mkdir work dir, no clone }` before `cloneOrPull`. Everything downstream
  (adapter Prepare → preflight → env → build(no-op) → up(pull) → ps → route → health) is unchanged.
- **Create handler** (`handler/apps.go:160`): when `build_kind == image` (or a `source=image`
  flag), skip the `gitURLRe` requirement (`apps.go:177`) and persist via a store path that writes
  empty `git_url`/`git_branch` + `source='image'` — mirror `CreateTemplateApp` (`store/apps.go:77`).
  `normalizeBuildConfig` already validates the `image`/`port` fields via `adapter.Validate`.
- **Store**: add `AppSourceImage = "image"` (`store/apps.go:19`); a `CreateImageApp` helper (or
  extend `CreateApp` to accept source). No migration needed — `source` and `build_config` columns
  already exist; the only schema add is Phase 2's credentials column.

## Phase 2 — Private-registry auth

The real new mechanism. A private image needs a `docker login` before the pull.

- **Storage**: new nullable column on `apps`, `registry_auth_enc BYTEA` (migration), holding a
  `crypto.Box`-sealed JSON `{registry, username, password}`. Sealed at rest exactly like env vars
  / SSH keys (`crypto/aead.go:43`); never returned by the API (redacted on read like
  `●●●●`). NULL = public image, no login. Requires `VAC_MASTER_KEY` to store (same posture as TOTP
  / webhook URLs, deviations D8) — without it, storing creds returns a clear error and only public
  images work.
- **Login before up**: new `dockercli` verb `Login(ctx, registry, user, pass)` wrapping
  `docker login` (password on stdin, never argv), and `Pull(ctx, projectDir, composeFile,
  projectName)` wrapping `docker compose pull`. In the pipeline, when the app has sealed creds:
  `Box.Open` → `docker login {registry}` → `docker compose pull` → existing `up`. Login writes to
  the daemon's `~/.docker/config.json`; we keep it scoped (one registry) and could `docker logout`
  after pull as a hardening follow-up.
- **Why login-then-pull and not compose `x-`/secrets**: compose has no first-class per-service
  registry-auth field; the daemon resolves pull auth from its config file. `docker login` is the
  supported path and keeps the generated compose credential-free (so the preflight'd file and the
  on-disk work tree never contain a secret).
- **UI**: the image card (Phase 3) gains an optional "private registry" disclosure — registry host
  (prefilled from the image ref's registry), username, password/token. Submitted to a sealed-write
  endpoint; rendered redacted on edit (reuse the env-var sensitive-value pattern, deviations D9).

## Phase 3 — Create-app UI

- **Add an `image` card** to `KIND_OPTIONS` in `BuildSourcePicker`
  (`ui/src/features/apps/build-source.tsx:22`) with a `Box`/`Container` glyph. When selected, the
  Source step swaps the **Git URL field** (`new-app.tsx` SourceStep) for an **image ref** input
  (`ghcr.io/me/app:tag`) + an internal-port input, plus the optional private-registry disclosure
  (Phase 2). No branch field.
- The wizard assembles `build_config = { image, port }` and posts `source: 'image'` /
  `build_kind: 'image'`; `compose_file` is irrelevant (back-fill empty). Types: add `'image'` to
  `BuildKind` and `image`/`port` to `BuildConfig` in `ui/src/types/api.ts`.
- App-detail keeps working: status, logs, stats, exec, domains, redeploy button — all read from
  the service rows the pipeline already upserts. The "Deploy" button re-pulls.

## Out of scope (explicitly)

- **Auto-update / new-digest polling** — v1 redeploys on operator action only. A background
  "watch `:latest` for a new digest and auto-deploy" (Watchtower-style) is a separate feature; note
  the preflight already flags bundled watchtower-style daemons (`compose/preflight.go`
  `daemonImageNeedles`), and we are not becoming one.
- **Multi-service image stacks** — that's the compose adapter. One image = one `app` service.
- **Per-service registry creds / multiple registries per app** — one image, one (optional) login.
- **Registry-credential reuse across apps** — creds are per-app, sealed per-app. A shared
  "registry credentials" store is a later refinement if anyone runs many apps off one private
  registry.
- **Image signature / provenance verification (cosign, etc.)** — pull and run; trust is the
  operator's.

## Rough size

- Phase 1: 1 adapter file, 1 `For` case, 1 `Validate` case, 2 BuildConfig fields, 1 pipeline
  clone-skip branch, 1 store source const + create path, create-handler branch. Small–medium — the
  generated-compose + clone-skip is mechanical; matching the template-app create path is the care.
- Phase 2: 1 migration (1 column), 2 `dockercli` verbs (`Login`, `Pull`), seal/open + pipeline
  login-then-pull, 1 sealed-write endpoint + redaction. Medium — the new auth path is the real work.
- Phase 3: 1 UI card + a swapped Source-step field + optional creds disclosure, 2 type additions.
  Small.

## Build order

1. `adapter/image.go` + `KindImage` const + `For` case + `Validate` case + BuildConfig fields.
2. `AppSourceImage` const + create store path (empty git_url, `source=image`); adapter unit test
   asserting the generated compose has `image:`, `expose:`, no `build:`, no `ports:`.
3. Pipeline clone-skip branch for `source=image`; create-handler branch dropping the git_url
   requirement. End-to-end deploy of a **public** image (pull → expose → route → Caddy health).
4. `registry_auth_enc` migration + seal/open + `dockercli.Login`/`Pull` + pipeline login-then-pull;
   sealed-write endpoint + redaction.
5. UI: `image` card, swapped Source-step fields, private-registry disclosure, type additions.
6. `/code-review` + `/simplify`; `/refresh-kb` (new `adapter/image.go`, new source mode →
   `architecture.md`/`deployment-flow.md` touched).

## Verification

- A public `ghcr.io` image with `port: 8080` deploys: pulled, `{slug}--app` attached to vac-edge,
  Caddy routes and gates health, app reaches `running` with no commit recorded.
- A port-less image deploys as a worker: up, no route, not flipped to degraded for lack of a port.
- A private image with stored creds pulls after `docker login`; the same image with no/wrong creds
  fails the pull as a transparent deploy error (prior stack keeps serving — invariant).
- Redeploy on a moved tag pulls the new digest; a `@sha256:` ref is stable across redeploys.
- The credentials column never appears in any API response; `GET /apps/{id}` redacts it.
- `make lint typecheck test` clean.
