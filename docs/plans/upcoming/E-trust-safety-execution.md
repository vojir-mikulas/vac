# Track E — Trust & Safety — execution plan

> Working plan for executing **Track E** of [`00-parallel-tracks.md`](00-parallel-tracks.md).
> Track E carries the two recently-captured **trust-moat** stubs:
> **E1 `16` Compose preflight validation** and **E2 `15` Security dashboard**.
>
> Unlike a normal track, the two items are **file-disjoint** and can be split across **two
> agents**: E1 lives in `api/internal/compose` + one additive gate in `deploy/pipeline.go`;
> E2 lives in a new `api/internal/security` package + a new UI tab, reusing Track B's already-
> shipped `reqmetrics`/`stats`/`notify` plumbing. They're listed in **priority order** (E1
> first — it hardens the deploy loop), not because they share files.
>
> Sequence (single operator): **E1 `16` → E2 `15`.** In parallel: run them concurrently.
> Owns: `api/internal/compose/preflight.go`, one insertion point in `deploy/pipeline.go`, the
> new `api/internal/security` package, one additive observer hook on `reqmetrics.Collector`,
> a new `EventTrafficAnomaly` notify event, a new Security handler + route, and a new
> `ui/src/features/security/` tab.
>
> **Strategy framing (from the stubs + `00`):** the moat is *simplicity + UX + reliability +
> trust*, not feature count. E1 stops the deploy loop from silently accepting foot-guns
> (bundled proxies, host-port grabs, `docker.sock` mounts); E2 turns VAC's two best vantage
> points — **Caddy** (sees every request) and the **build pipeline** (builds every image) —
> into a read-only "your box at a glance" view. **Neither mutates host state** (E2 is read-only
> by design), so the control plane stays sandboxed (off `vac-edge`, out of privileged host
> mutation).

## Pre-flight: locked decisions (resolve the stubs' open questions before coding)

Decided up front so nothing churns mid-track.

1. **Migration range — reserve `00050`–`00059` for Track E.** Highest migration on this branch
   is `00042_addon_installs.sql` (Track D); Track D reserved `00040`–`00049`. Track E starts at
   `00050` so it never rebases D's in-flight numbers at merge. **E1 needs *no* migration** (its
   one new knob is a field on the existing `build_config` JSONB — decision #2). **E2 needs *no*
   migration in Phase 1** (anomaly detection is in-process; posture/fw/fail2ban are computed
   read-only on request). Reserve `00050_security_events.sql` for the *optional* Phase-2
   anomaly-history panel only — don't create it in v1.

2. **E1 escape hatch is a per-app `build_config` flag, split by risk class — not one master
   switch.** Add `AllowUnsafeCompose bool` (`json:"allow_unsafe_compose,omitempty"`) to
   `adapter.BuildConfig` (`adapter.go:39-52`), parsed by the existing `ParseConfig`
   (`adapter.go:55`). It downgrades the **"VAC owns the edge"** error class
   (`edge_port_conflict`, `bundled_reverse_proxy`) to logged warnings — those are *VAC-
   incompatibility*, the operator's call to override. It does **not** downgrade the **host-
   escape** class (`docker_socket_mount`, `privileged_or_host_net`): those hand app code host-
   root on the control box, the exact thing VAC's isolation exists to prevent, and stay hard
   regardless (resolves the stub's open question — *yes, `docker.sock` is an absolute hard-no*).
   Every finding is logged at every gate, so the deploy log is always the source of truth.

3. **E1 runs on the *resolved* compose, after `Prepare`, before `Build`.** Insert the gate in
   `deploy/pipeline.go` immediately after `composeFile, err := ad.Prepare(...)` succeeds
   (`pipeline.go:238`) and before the env-file setup / `Build` (`pipeline.go:256-268`) — the
   same slot the stub names. Running on the *adapter-resolved* file means adapter-generated
   compose wraps (Dockerfile/framework/static kinds) are covered too, not just user-authored
   `compose.yaml`. The block/log split mirrors the two idioms already in the file: hard findings
   take the **`WaitHealthy` failure shape** (`pipeline.go:330-341` — `logSystem` →
   `MarkDeploymentFinished(…, DeploymentStatusError, &msg)` → `SetAppStatus(…, AppStatusDegraded)`
   → `Notifier.DeployFailed(…)` → `return nil`); warnings take the **`WarnIfMissingDockerignore`
   shape** (`pipeline.go:250-253` — `p.logSystem` per finding, deploy proceeds).

4. **E1 keeps `compose.Service` lean — preflight gets its own richer parse pass.** The existing
   `Service` struct (`compose/parse.go:17-22`) deliberately exposes only `Name/Image/HasBuild/
   Ports` and its doc comment promises to stay that way. Don't widen it. Add a private
   `preflightView` via a second `yaml.Unmarshal` (`gopkg.in/yaml.v3`, already imported) inside
   `preflight.go`, normalizing list- *and* map-form `labels`/`ports`/`networks` and the top-
   level `networks:`/`volumes:` maps. Matcher tables (proxy images, daemon images, edge ports)
   are package-level vars for easy extension + unit-testing.

5. **E2's traffic panel reuses the *existing* Caddy access-log tail — no new logging to turn on,
   no second file handle.** `reqmetrics` already tails `/var/log/caddy/access.log`
   (`reqmetrics/tailer.go:21-74`, 1 s poll, rotation-safe) and Caddy already writes JSON access
   logs (`caddy/config.go:178-179`). The security analyzer must **not** open a second tail.
   Instead, add **one additive observer hook** to `reqmetrics.Collector` (a `func(accessLine)`
   field, default nil) so each parsed line is fanned out to the security monitor for free. This
   is a one-field, backward-compatible touch on Track B's shipped code — flag it at merge, but
   it rewrites nothing. (Alternative considered and rejected: a second independent `Tail` on the
   same file — simpler to isolate but doubles the poll + parse cost for no benefit.)

6. **E2 is read-only everywhere; the anomaly detector runs always-on but cheap; posture / fw /
   fail2ban are computed on-request.** The four panels split by cost:
   - **Anomaly detector** must run continuously (an attack isn't only happening while someone
     watches the dashboard), so it can't be subscriber-gated like `stats`. Keep it within the
     RAM budget by riding the existing tail (decision #5) and holding only **bounded streaming
     counters** — reuse the sliding-`window` pattern from `crashloop/monitor.go:302-325`
     (`[]time.Time` + `trim`/`size`), capped per-IP with LRU eviction. No persistence in v1.
   - **Posture checklist, firewall view, fail2ban status** are pulled **on each GET** to the
     Security API — pure reads, zero background cost, nothing to gate.
   - Gate the always-on analyzer behind `VAC_SECURITY_MONITOR` (**default on**; it's nearly
     free). fail2ban/firewall reads **capability-detect and degrade**: if `fail2ban-client` /
     `ufw` / `nft` aren't present or readable, the panel shows "not detected" rather than
     erroring (resolves the stub's open question — *no privileged helper required in v1*; a
     helper is a deferred decision only if read access turns out to need root).

7. **E2 adds no host mutation and no new privileged surface.** No ban/unban, no firewall edits,
   no Trivy/Grype CVE scan, no SAST — all explicitly **out** per the stub. The control plane
   stays off `vac-edge` and runs only *read* syscalls/exec. This is the load-bearing trust
   decision: a security dashboard that can't brick the box.

---

## E1 — `16` Compose preflight validation  *(effort M)*

**Goal:** before VAC builds and `up`s a user's compose, run a **preflight lint** that classifies
known-incompatible constructs into **hard errors** (block with a clear message) and **warnings**
(deploy, but explain the consequence). Teach the operator *why* their compose won't work on VAC —
never silently mutate their file. Full rationale + rule catalog live in the stub
([`16-compose-preflight-validation.md`](16-compose-preflight-validation.md)); this plan locks the
*where* and *how*.

### Design decisions (locked — see pre-flight #2–#4)

- Own richer parse pass (`preflightView`), `compose.Service` stays lean (#4).
- Gate after `Prepare`, before `Build`, block/log split mirroring the existing idioms (#3).
- Per-app `allow_unsafe_compose` downgrades the edge class only; host-escape stays hard (#2).
- No migration — the one knob is a `build_config` field (#1).

### New file: `api/internal/compose/preflight.go` (+ `preflight_test.go`)

Same package as `Parse` (`compose/parse.go`). Public surface:

```go
type Severity int
const ( SeverityWarn Severity = iota; SeverityError )

type Finding struct {
    Severity Severity
    Code     string // stable id, e.g. "edge_port_conflict" — for tests + future UI
    Service  string // "" for stack-level
    Message  string // operator-facing: what + why + the fix
}

// Preflight parses the resolved compose file with a richer view than compose.Service
// and returns all findings (errors + warnings), unsorted by severity.
func Preflight(composeFile string) ([]Finding, error)
```

Internals:
- `preflightView` (private) — a second `yaml.Unmarshal` capturing `command`, `ports`
  (host/target/proto), `expose`, `networks`, `network_mode`, `volumes`, `labels`,
  `container_name`, `privileged`, `cap_add`, plus the top-level `networks:`/`volumes:` maps.
  Normalizers handle list- vs map-form for `labels`/`ports`/`networks` (compose accepts both).
- Package-level matcher tables: `proxyImages` (`traefik`, `caddy`, conditional `nginx`),
  `daemonImages` (`containrrr/watchtower`, `ouroboros`, …), `edgePorts` (`80`, `443`).
- One small `func(preflightView) *Finding` per rule, run over every service + once at stack level.

**Rule catalog** (verbatim from the stub — implement exactly):

| Severity | Code | Detection (gist) |
|---|---|---|
| error | `edge_port_conflict` | publishes host `80`/`443` |
| error | `bundled_reverse_proxy` | image ∈ {traefik, caddy}; or `traefik.*` label; or `--certificatesresolvers`/`--entrypoints` in command; or `nginx` **only when** it also binds 80/443 or carries proxy labels |
| error | `docker_socket_mount` | volume source is `…/docker.sock` |
| error | `privileged_or_host_net` | `privileged: true`, `network_mode: host`, or `cap_add ⊇ {SYS_ADMIN, ALL}` |
| warn | `host_port_publish` | any host `ports:` mapping other than 80/443 |
| warn | `fixed_container_name` | `container_name:` set |
| warn | `lifecycle_daemon` | image ∈ daemon table or watchtower labels |
| warn | `no_routable_http` | no service exposes an HTTP-ish internal port |

### Adapter: the escape-hatch field

`api/internal/adapter/adapter.go` — add to `BuildConfig` (lines 39-52):

```go
AllowUnsafeCompose bool `json:"allow_unsafe_compose,omitempty"`
```

`ParseConfig` (`adapter.go:55`) already unmarshals the whole `build_config` JSONB, so the field is
populated for free. Per decision #2 it downgrades only `edge_port_conflict` +
`bundled_reverse_proxy`.

### Pipeline wiring: the one additive gate

`api/internal/deploy/pipeline.go`, inserted right after `composeFile, err := ad.Prepare(...)`
(`pipeline.go:238`), before the env-file setup at line 256:

```go
findings, perr := compose.Preflight(composeFile)
if perr != nil {
    // parse failure is informational, not a hard block — log and proceed (build will fail loudly)
    _ = p.logSystem(ctx, deploymentID, "compose preflight skipped: "+perr.Error())
} else {
    var blocking []compose.Finding
    for _, f := range findings {
        _ = p.logSystem(ctx, deploymentID, formatFinding(f)) // always log every finding
        if f.Severity == compose.SeverityError && !downgraded(f, cfg.AllowUnsafeCompose) {
            blocking = append(blocking, f)
        }
    }
    if len(blocking) > 0 {
        msg := "compose preflight failed:\n" + joinFindings(blocking)
        _ = p.logSystem(ctx, deploymentID, msg)
        _ = p.Store.MarkDeploymentFinished(ctx, deploymentID, DeploymentStatusError, &msg)
        _ = p.Store.SetAppStatus(ctx, app.ID, AppStatusDegraded)
        markHTTPServicesDegraded(ctx, p.Store, app.ID)
        p.Notifier.DeployFailed(app.Name, app.ID, msg, time.Since(runStart))
        return nil // same transparent-failure shape as the WaitHealthy block (pipeline.go:330-341)
    }
}
```

`downgraded(f, allow)` returns true when `allow && f.Code ∈ {edge_port_conflict,
bundled_reverse_proxy}` (host-escape codes never downgrade). `formatFinding`/`joinFindings` are
small local helpers (service + code + message). **No build, no `up` when blocked** — the prior
running stack keeps serving (the invariant: deploy failure never tears down the running stack).

### Surfacing (Phase 1 only)

Reuses what the UI already renders — **no UI work required for E1**:
- Each finding → a deploy **system log line** (`logSystem` → `DeploymentLogStreamSystem`), shown
  live in the existing `BuildLogs`/`LogViewer` (`deploys-tab.tsx:164-178`).
- A hard block sets `deployment.error` to the combined message, already rendered as the styled
  error paragraph (`deploys-tab.tsx:110-114`).
- (Deferred Phase 2, optional) persist findings as structured rows and return them on the deploy-
  detail API so a "compose issues" panel can render by `Code` without string-matching. The `Code`
  field exists precisely for this.

### Tests

- `preflight_test.go`, table-driven: one positive + one negative case per rule code.
- **Fixture: the full `dej-prijimacky` compose** from the stub, asserting the exact finding set:
  `edge_port_conflict` + `bundled_reverse_proxy` (traefik) + `docker_socket_mount` (traefik +
  watchtower) + `host_port_publish` (postgres/redis/gotenberg) + `fixed_container_name` (×4) +
  `lifecycle_daemon` (watchtower).
- Normalization tests: list-form vs map-form `labels`/`ports`/`networks`.
- Pipeline-level test: a hard finding blocks **before** Build (assert Build is never called);
  `allow_unsafe_compose:true` downgrades `edge_port_conflict` to a warning but **not**
  `docker_socket_mount`.

### Acceptance

- Deploying a compose that binds 80/443, bundles Traefik/Caddy, or mounts `docker.sock` is
  blocked **before Build** with a message naming the offending service + the fix; the running
  stack keeps serving.
- Deploying a compose with stray host ports / `container_name` / Watchtower proceeds but logs a
  warning per finding.
- `allow_unsafe_compose:true` downgrades the edge-conflict errors to logged warnings; the
  host-escape errors still block.

### Files touched

`api/internal/compose/preflight.go` + `preflight_test.go` (new),
`api/internal/adapter/adapter.go` (`AllowUnsafeCompose` field),
`api/internal/deploy/pipeline.go` (the one additive gate after `Prepare`, ~line 238) + a pipeline
test. **No new migration, no store change, no UI change in Phase 1.** The single
`deploy/pipeline.go` touch is the only Track-A-adjacent edit — see cross-track sync.

---

## E2 — `15` Security dashboard  *(effort M)*

**Goal:** a read-only "Security" tab surfacing the box's posture and suspicious traffic. VAC
*shows and alerts*; the operator acts. Four panels, all read-only, control plane stays sandboxed.
Full rationale in the stub ([`15-security-dashboard.md`](15-security-dashboard.md)); this plan
locks the seams.

### Design decisions (locked — see pre-flight #5–#7)

- Traffic panel rides the existing Caddy access-log tail via a one-field observer hook on
  `reqmetrics.Collector` — no new tail, no Caddy-logging change (#5).
- Anomaly detector is always-on but bounded (sliding-window counters, per-IP LRU) (#6).
- Posture / firewall / fail2ban are computed on-request; fw/fail2ban capability-detect (#6).
- Read-only everywhere; no host mutation, no CVE/SAST (#7). No migration in v1 (#1).

### The four panels

1. **Posture checklist** *(easiest, most on-brand)* — a static rules pass over VAC's *own* config
   and store: any app published on a host port (cross-reference the E1 `host_port_publish` signal
   + `services` rows), apps missing TLS, `VAC_MASTER_KEY` present, metrics endpoint token set,
   default/weak settings. Pure rules engine, no external deps, computed per GET.
2. **Traffic anomaly / DDoS signals** *(highest value-per-effort)* — in-process rolling per-IP /
   per-app counters fed by the access-log observer: RPS spikes, 4xx/5xx surges, top talkers, odd
   UAs/paths. Threshold breach → `notify` alert (reusing Discord/Slack). Streaming counters,
   bounded RAM.
3. **fail2ban status (read-only)** — parse `fail2ban-client status <jail>` output (banned IPs,
   jail counts). Display only. Capability-detect; "not detected" when absent.
4. **Firewall view (read-only)** — show host `ufw`/`nftables` rules + open ports. Display only.
   Capability-detect.

### Access-line enrichment (the only Track-B touch)

`reqmetrics` currently parses just `request.host` + `status` + size (`collector.go:69-76`,
`accessLine` struct). The security analyzer additionally needs the **client IP**, **URI**, and
**User-Agent**, which Caddy's default JSON access log already emits (`request.client_ip` /
`request.remote_ip`, `request.uri`, `request.headers.User-Agent`). Two-part change, both additive:

1. Widen `reqmetrics`'s `accessLine` struct to also decode those fields (it already decodes the
   JSON line; this adds fields, breaks nothing). **Verify** the fields are present in the live
   Caddy JSON output first; if `client_ip`/User-Agent are stripped by the current encoder config
   (`caddy/config.go:178-179`), add them to the access-log format there — a config-only,
   additive change pushed via the existing `BaseConfig` boot path (`main.go:346-356`). *(Open
   item to confirm at code time; Caddy's default JSON includes all three, so likely no Caddy
   change.)*
2. Add an optional observer hook to `reqmetrics.Collector`: a `func(line accessLine)` field
   (default nil), invoked per parsed line alongside the existing bucket aggregation
   (`collector.go:36-77`). The security monitor registers itself; aggregation is untouched.

### Backend: new package `api/internal/security`

1. **`security.Monitor`** — receives access lines from the observer hook, maintains bounded
   counters reusing the `crashloop` sliding-`window` pattern (`crashloop/monitor.go:302-325`):
   per-IP and per-app request/error windows, top-talker LRU (cap N IPs, evict oldest), suspicious
   UA/path matchers. A small evaluator checks thresholds each window tick and, on breach, calls
   the notifier **once per cooldown** (debounced like crashloop's trip). Exposes a `Snapshot()`
   for the dashboard (top talkers, current rates, recent anomalies). Always-on, gated by
   `VAC_SECURITY_MONITOR` (default on).
2. **`security.Posture`** — `Check(ctx) []PostureFinding` — pure read over store + config (apps,
   services, host ports, TLS state, master-key/metrics-token presence). Reuses the same
   `Severity`/`Code` shape as E1's `compose.Finding` for UI consistency.
3. **`security.Host`** — `Fail2ban(ctx)` and `Firewall(ctx)` — capability-detect + read-only
   exec (`fail2ban-client status`, `ufw status` / `nft list ruleset`), parse, return structured
   state or a "not detected" marker. No mutation.
4. **Notify:** add `EventTrafficAnomaly` to `notify/events.go` (`events.go:13-21` constants +
   the `AllEvents` slice at line 25) and `Dispatcher.TrafficAnomaly(appName, appID, kind,
   detail string)` to `notify/dispatcher.go`, mirroring the `CrashLoop` shape
   (`dispatcher.go:268-278` → `dispatch(Event)`, fire-and-forget, toggle-gated).

### Wiring (`main.go`)

Alongside the existing `reqmetrics`/`stats` init (`main.go:198-208`): construct
`security.NewMonitor(...)`, register its handler on the `reqmetrics.Collector` observer hook, wire
the `notify.Dispatcher`, and `go monitor.Run(ctx)` gated by `cfg.SecurityMonitor` (the
`if cfg.X { go … }` pattern from `main.go:256-262`). Posture/host readers are constructed for the
handler; no goroutine.

### Config

`api/internal/config/config.go` — add `SecurityMonitor bool` to the `Config` struct (lines
30-102) and parse `VAC_SECURITY_MONITOR` in `applyEnv` (lines 174-324), default **true**,
mirroring the `VAC_MANAGED_SERVICES` bool parse (`config.go:318-320`). Optional threshold knobs
(`VAC_SECURITY_RPS_THRESHOLD`, etc.) follow the same pattern with sensible defaults that don't
false-positive on a busy small app.

### HTTP + audit

`api/internal/server/handler/security.go`, mounted in the authenticated `/api` group
(`server.go:161` group, `RequireSession` at line 181):
- `GET /api/security/posture` → posture findings
- `GET /api/security/traffic` → monitor snapshot (top talkers, rates, recent anomalies)
- `GET /api/security/fail2ban` → fail2ban state or "not detected"
- `GET /api/security/firewall` → firewall rules or "not detected"

All read-only ⇒ no `audit.SetTarget`/`Describe` enrichment needed (the Stage-0 audit middleware
still records the GETs for free). No CSRF concerns (no mutations).

### UI: new feature `ui/src/features/security/`

- **Security tab** — new route `ui/src/routes/_app/security.tsx`:
  `createFileRoute('/_app/security')({ component: SecurityPage })` (the pattern from the other
  `_app/*` routes). Add a nav entry to the `NAV` array in
  `ui/src/components/layout/sidebar.tsx:9-18` (`{ to: '/security', label: 'Security', icon:
  ShieldCheck }`).
- **`security-page.tsx`** — `PageContainer`/`PageHeader` shell (from `app-shell.tsx`), four
  sections via `SectionHeader`:
  - Posture: a checklist of pass/fail rows reusing `StatusPill` tones (`status-pill.tsx`).
  - Traffic: `StatTile`/`StatStrip` for current RPS / error-rate / top-talker count, a top-talkers
    table, and a recent-anomalies list. (Optionally a sparkline reusing the request-series the
    app-detail metrics already chart.)
  - fail2ban + firewall: read-only tables, or an `EmptyState` "not detected on this host".
- **`ui/src/lib/api/security.ts`** — `securityApi` (raw `get` calls via `lib/api/client.ts`) +
  `useSecurityPosture`/`useSecurityTraffic`/`useFail2ban`/`useFirewall` TanStack Query hooks;
  add `security.*` keys to `ui/src/lib/query/keys.ts`. Traffic hook uses a short `refetchInterval`
  (e.g. 5 s) for a live feel without a new WebSocket. Types in `ui/src/types/api.ts`
  (`PostureFinding`, `TrafficSnapshot`, `Fail2banState`, `FirewallState`).

### Tests

- `security.Monitor`: feed synthetic access lines → assert top-talker tracking, window trim, a
  4xx/5xx surge trips `TrafficAnomaly` once (cooldown holds), LRU caps per-IP entries.
- `security.Posture`: store fixtures → asserts each rule fires (app on host port, missing TLS,
  master key absent).
- `security.Host`: capability-detect returns "not detected" when the binary is absent (no error);
  parses a captured `fail2ban-client`/`ufw` output fixture when present.
- Handler: each GET returns the expected shape; endpoints are read-only (no mutation paths).
- `reqmetrics`: observer hook fires per line and does **not** alter existing bucket aggregation.

### Acceptance

The Security tab shows the posture checklist (pass/fail), a live traffic panel (per-IP rates +
anomaly flags), and — if present on the host — read-only fail2ban + firewall state. A traffic
anomaly fires a notification. **Nothing in this cut mutates host state.**

### Files touched

`api/internal/security/*` (new: `monitor.go`, `posture.go`, `host.go` + tests),
`api/internal/reqmetrics/collector.go` (widen `accessLine`, add observer hook — **the one
Track-B touch**), `api/internal/notify/{events,dispatcher}.go` (`TrafficAnomaly`),
`api/internal/config/config.go` (`SecurityMonitor` + thresholds),
`api/internal/server/handler/security.go` (new) + route wiring in `server.go`,
`api/main.go` (monitor wiring + gated goroutine),
plus UI: `features/security/*`, `lib/api/security.ts`, `lib/query/keys.ts`, `types/api.ts`,
`components/layout/sidebar.tsx`, new route. *(Possibly `caddy/config.go` access-log format — only
if the default JSON omits client IP / User-Agent; confirm first.)* **No new migration in v1.**

---

## Cross-track sync points (from `00`)

- **Migrations:** Track E owns `00050`–`00059`. Highest on `main` is `00042` (Track D);
  Track D reserved `00040`–`00049`. **E uses zero migrations in v1** — the gap is pure safety
  margin against D's in-flight work. Reserve `00050_security_events.sql` only for the optional
  Phase-2 anomaly-history panel.
- **E1 `16` ↔ Track A `05`/A3 (deferred):** E1 inserts a **single additive gate** into
  `deploy/pipeline.go` right after `Prepare` (line 238), before Build — it does **not** touch the
  up→health→swap path A3 rewrites. A3 is parked; if it resumes, **land E1 first** (or rebase its
  one insertion). This is the only point where Track E touches Track A's hot path. Flag it at the
  merge PR so A's owner sanity-checks the insertion slot.
- **E2 `15` ↔ Track B (done):** E2 reuses the shipped `reqmetrics` tail + `stats` request-rate +
  `notify` plumbing. The **only** Track-B edit is additive: widen `reqmetrics.accessLine` and add
  one observer-hook field on `Collector` (`collector.go`) — aggregation, the `request_metrics`
  table, and the frozen `vac_*` metric names are all untouched. Flag the hook at merge.
- **E1 ↔ E2:** file-disjoint (compose/pipeline + adapter vs. security-pkg/reqmetrics/notify/UI).
  They share only the `Severity`/`Code`/`Finding` *shape* — define it once in `compose` for E1;
  E2's posture mirrors the same field names for UI consistency (copy the shape, don't import
  across package boundaries unless it reads cleanly). Safe to build in parallel by two agents.

## Strategy gate (build now; both are trust-moat, ship when stable)

Per `00`'s critical path, **Track E is the live priority** for the trust moat. E1 has no
shipping gate — it only *adds* safety to the deploy loop and can land as soon as it's reviewed.
E2 is read-only and additive; `VAC_SECURITY_MONITOR` defaults on but is cheap, and fail2ban/
firewall degrade gracefully, so it's safe to ship incrementally panel-by-panel (posture +
traffic first; fail2ban/firewall when host-read access is confirmed). If a track must slip, the
`00` order holds: **slip D before E before F** — protect the trust-moat work.

## Suggested commits (Conventional Commit, commitlint-compatible)

- `feat(compose): preflight lint that blocks/warns on VAC-incompatible compose` (E1)
- `feat(security): read-only posture + traffic-anomaly dashboard` (E2)

Run `/code-review` + `/simplify` after each, and `/refresh-kb` at the end — Track E adds a
`security` package, a `compose.Preflight` gate in the deploy pipeline, and a `reqmetrics`
observer hook, so `architecture.md` and `deployment-flow.md` both need regenerating. Log the
preflight gate (and the "reject/warn, never silently rewrite" stance) and the read-only
security-dashboard posture in `docs/deviations.md`.
