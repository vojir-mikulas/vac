# 16 — Compose preflight validation (reject/warn, don't silently rewrite)

**Tier:** Trust & UX · **Effort:** M · **Status:** stub

## Goal

Before VAC builds and `up`s a user's compose, run a **preflight lint** that classifies
known-incompatible constructs into **hard errors** (block the deploy with a clear message)
and **warnings** (deploy, but explain the consequence). The point is to teach the operator
*why* their compose won't work on VAC — not to silently mutate their file.

## Why it matters (strategy)

VAC's moat is simplicity + UX + reliability + trust. Today VAC runs the user's compose almost
verbatim (only an additive RAM-limit `-f` override — see `pipeline.go` Build/Up). A compose
that bundles its own edge (Traefik/Caddy/nginx), publishes host `80`/`443`, or mounts the
Docker socket collides with VAC's invariants and fails opaquely at `docker compose up`, or —
worse — succeeds and quietly grants app code host-root via `/var/run/docker.sock`.

Silently *rewriting* the compose is the wrong fix: it breaks the operator's mental model (what
runs no longer matches what they committed), forces VAC to guess intent (is `5432:5432`
deliberate?), and contradicts VAC's transparent-failure posture ("failure is recorded as
state, not a rollback"). A loud, explainable preflight surfaces the conflict instead of hiding
it — and stays honest about VAC being the edge.

This is the motivating real-world example (a GCP/Traefik/Watchtower stack) that prompted the
plan: it would trip `edge_port_conflict` + `bundled_reverse_proxy` (traefik),
`docker_socket_mount` (traefik + watchtower), `host_port_publish` (postgres/redis/gotenberg),
`fixed_container_name` (×4), and `lifecycle_daemon` (watchtower).

## Architecture invariants this enforces

- **Caddy owns the edge** (TLS + 80/443). Host bindings on 80/443 conflict with `vac-proxy`.
- **Routing is by DNS alias on `vac-edge`, not host ports.** Host `ports:` bypass Caddy.
- **`vac-api`/control plane isolation.** Docker-socket mounts hand app code host-root on the
  control box — the opposite of the isolation VAC is built around.

## Rough shape

### Where it lives & runs

- New `api/internal/compose/preflight.go` (+ `preflight_test.go`), same package as `Parse`.
- Called in `deploy/pipeline.go` **right after `ad.Prepare(...)` resolves `composeFile`
  (~line 213) and before Build** — gates the expensive build/up, runs on the *resolved*
  compose (so adapter-generated wraps are covered too).
- Mirrors the two-tier idiom already in the pipeline:
  - **Hard errors** → return early; `MarkDeploymentFinished(..., DeploymentStatusError, &msg)`,
    `SetAppStatus(..., degraded)`, notify — same shape as the `WaitHealthy` failure block
    (`pipeline.go:306-317`). No build, no `up`.
  - **Warnings** → `p.logSystem(...)` per finding, same as `WarnIfMissingDockerignore`
    (`pipeline.go:226-228`). Deploy proceeds.

### Data model

`compose.Service` today only has `Name/Image/HasBuild/Ports` and its doc comment promises to
stay lean ("only the three things the pipeline needs"). Keep it lean — add a **dedicated
richer view** for preflight via a second `yaml.Unmarshal` pass:

```go
type preflightView struct {
    name          string
    image         string
    command       []string          // string- or list-form
    ports         []portMapping     // host + target + protocol
    expose        []string
    networks      []string          // names or map keys
    networkMode   string
    volumes       []string          // raw source:target[:opts]
    labels        map[string]string // normalized from list- or map-form
    containerName string
    privileged    bool
    capAdd        []string
}
```

Compose accepts both list- and map-form for `labels`, `ports`, and `networks` — normalize
both. Also parse top-level `networks:` / `volumes:` maps.

### Result type

```go
type Severity int
const ( SeverityWarn Severity = iota; SeverityError )

type Finding struct {
    Severity Severity
    Code     string   // stable id, e.g. "edge_port_conflict" — for tests + future UI
    Service  string   // "" for stack-level
    Message  string   // operator-facing: what + why + the fix
}

func Preflight(composeFile string) ([]Finding, error)
```

The pipeline splits on severity: any `SeverityError` ⇒ block with a combined message; warns ⇒
log each. Keep matcher tables (proxy images, daemon images, edge ports) as package-level vars
for easy extension + unit-testing.

## Rule catalog

### Hard errors (block)

| Code | Detection | Message gist |
|---|---|---|
| `edge_port_conflict` | any service publishes host port `80` or `443` | VAC's Caddy owns 80/443; remove host bindings — VAC terminates TLS for you |
| `bundled_reverse_proxy` | image base ∈ {`traefik`, `caddy`}; **or** any `traefik.*` label; **or** command contains `--certificatesresolvers`/`--entrypoints`; **or** `nginx` *only when* it also binds 80/443 or carries proxy labels | VAC is the edge; remove the bundled proxy/ACME |
| `docker_socket_mount` | volume source is `/var/run/docker.sock` (or `…/docker.sock`) | Mounting the Docker socket grants host-root to app code on the control box |
| `privileged_or_host_net` | `privileged: true`, `network_mode: host`, or `cap_add` ⊇ {`SYS_ADMIN`,`ALL`} | Privileged/host-network containers can escape VAC's isolation |

### Warnings (log, proceed)

| Code | Detection | Message gist |
|---|---|---|
| `host_port_publish` | any host `ports:` mapping other than 80/443 | host port bypasses Caddy and exposes it directly; prefer `expose` + a VAC domain |
| `fixed_container_name` | `container_name:` set | breaks project-scoped naming and can collide; VAC won't route it by alias |
| `lifecycle_daemon` | image base ∈ {`containrrr/watchtower`, `ouroboros`, …} or watchtower labels | VAC owns the deploy lifecycle; this will desync the dashboard |
| `no_routable_http` | no service exposes an HTTP-ish internal port | informational: no HTTP service detected; VAC won't assign a domain |

## Escape hatch (single-operator box)

The operator must be able to override their own judgment:

- Opt-in per-app flag `build_config.allow_unsafe_compose: true` (parsed in
  `adapter.ParseConfig`) that **downgrades hard errors to warnings** (still logged loudly).
- `docker_socket_mount` / `privileged_or_host_net` could stay hard even then, or require a
  separate explicit flag — **open question** (see below).
- Always log *every* finding regardless of gating, so the deploy log is the source of truth.

## Surfacing

- **Phase 1:** deploy log only (reuses `logSystem`) + the `error` deployment status carries the
  combined message, which the UI already renders.
- **Phase 2 (optional, later):** persist findings as structured rows / return them on the
  deploy detail API so the dashboard shows a "compose issues" panel. The `Code` field exists
  precisely so the UI can render/i18n without string-matching.

## Testing

- Table-driven `preflight_test.go`: one case per rule (positive + negative).
- **Fixture: the full `dej-prijimacky` compose** asserting the exact finding set listed in
  "Why it matters" above.
- Normalization tests for list-form vs map-form `labels`/`ports`/`networks`.
- Pipeline-level test: a hard finding blocks before Build (assert Build is never called).

## Suggested order of work

1. Richer parse (`preflightView` + normalizers) + tests.
2. `Finding`/`Severity`/`Preflight` + rule functions + the big fixture test.
3. Wire into `pipeline.go` (block/log split) + pipeline test.
4. `allow_unsafe_compose` escape hatch.
5. (Later) structured surfacing to the UI.

## Open questions

- **Is `docker.sock` an absolute hard-no, or overridable** via the escape hatch?
- **`nginx` is ambiguous** — legit app server *and* common bundled edge. Proposal: only flag it
  when it also binds 80/443 or carries proxy labels (as in the motivating example), not on
  sight.
- Where to draw the line on rewriting: the one defensible *transform* is the narrow, opt-in,
  override-file path already used for RAM limits (inject `vac-edge` attachment + neutralize
  host-port publishing for the designated HTTP service) — opt-in + transparent + reversible.
  Out of scope for Phase 1; capture as a follow-up if there's appetite.

## Acceptance (sketch)

- Deploying a compose that binds 80/443, bundles Traefik/Caddy, or mounts `docker.sock` is
  blocked before Build with a message that names the offending service and the fix.
- Deploying a compose with host ports / `container_name` / Watchtower proceeds but logs a
  warning per finding.
- `allow_unsafe_compose: true` downgrades (per the open-question decision) hard errors to
  logged warnings.
