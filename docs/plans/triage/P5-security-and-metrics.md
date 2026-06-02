# P5 — Security & Metrics (detailed plan)

**Track:** P5 (see [`00-parallel-tracks.md`](00-parallel-tracks.md)) · **Status:** ready to build
**Owns:** `internal/security` (`host.go`, `monitor.go`, `posture.go`), `internal/reqmetrics`
(`collector.go`, `scraper.go`, `tailer.go`), the security handler
(`server/handler/security.go`), the security UI (`ui/src/features/security/security-page.tsx`,
`ui/src/lib/api/security.ts`, `ui/src/types/api.ts`), the nav rail
(`ui/src/components/layout/sidebar.tsx`), and — for P5.2 only — a new host-side read helper +
its install/compose wiring.
**Source plan:** [security-and-metrics.md](security-and-metrics.md). Relates to
[`../upcoming/15-security-dashboard.md`](../upcoming/15-security-dashboard.md).

Sequence: **P5.1 (honest status) → P5.2 (privileged read helper)** are serial (P5.2 fills in the
states P5.1 defines). **P5.3 (metrics disabled message)** and **P5.4 (nav badge)** are independent
and can land in any order alongside the others. Recommended landing order: **P5.1 → P5.3 → P5.4 →
P5.2** — the first three are pure honesty/UX over existing data and ship cheaply; P5.2 is the one
real architectural change and can trail.

> **No migration.** Everything here is in-memory counters (`monitor.go`), config snapshots
> (`PostureConfig`), per-request host reads, or UI. Nothing touches the schema, so P5 claims **no
> number** from the `00060`+ range — leave those for P6/P1.

---

## Reality check (read before coding — two of the triage assumptions are wrong)

The triage notes ([security-and-metrics.md](security-and-metrics.md)) are directionally right
("things read as failing") but the *root causes* differ from what's written. Confirmed against
source:

### A. The monitor is **on by default**, not off — so "disabled" is the rare case

The source plan says request metrics are *"gated behind `VAC_SECURITY_MONITOR` — if unset, it's
`nil` → always empty."* That's backwards. `config.Default()` sets `SecurityMonitor: true`
(`config/config.go:159`); the env var only *overrides* (`config.go:354-356`). So unless the
operator explicitly set `VAC_SECURITY_MONITOR=false`, `secMonitor` is constructed and wired
(`main.go:217-225,238-241`) and `SecurityTrafficHandler` gets a live, non-nil monitor.

⇒ The empty traffic panel almost always means **the monitor is running but seeing no parseable
access-log lines**, not that it's disabled. So P5.3's "monitoring is disabled" message, while
correct to add, is the *less* common explanation. The higher-value honesty fix is distinguishing
**"no traffic yet"** from **"Caddy JSON access logging isn't reaching the collector."** Both render
identically today (empty `TopTalkers`/`TotalRequests`) — see P5.3.

The collector tails `cfg.CaddyAccessLog` (`/var/log/caddy/access.log`, mounted read-only at
`compose.prod.yaml:82`). If Caddy isn't writing JSON access logs to that path, every line fails
`json.Unmarshal` and is silently dropped (`collector.go:138-141`) — counters stay at zero with no
signal. That is the real "feels broken."

### B. fail2ban / ufw can **never** be detected from inside the container — the binaries aren't there

The source plan attributes "not detected" to the sandbox running host reads *non-root* against
root-owned sockets. True, but it misses the bigger wall: **`vac-api` runs in a Debian container
(`api/Dockerfile`) that installs only `git`, `openssh-client`, `gnupg`, and the Docker CLI** —
**not** `fail2ban-client`, `ufw`, or `nft`. So `binaryPresent("fail2ban-client")` /
`binaryPresent("ufw")` / `binaryPresent("nft")` (`host.go:67-70`, via `exec.LookPath`) return
`false` **regardless of what's installed on the host**, and `Fail2ban`/`Firewall` short-circuit to
`Detected:false` at `host.go:75` / `host.go:144,149`. The 3s/non-root/root-socket nuance never even
comes into play — the lookup fails first.

⇒ This is the crux: **from inside the sandbox the host firewall/fail2ban state is structurally
unreadable**, not "sometimes unreadable." Honest status (P5.1) therefore can't honestly say "not
installed" (VAC has no way to know) — it must say *"VAC can't inspect the host from inside its
sandbox; enable the read helper."* And P5.2 isn't optional polish — it's the **only** path by which
these panels can ever show real data. This reframes both items below.

### C. What's already correct and should be left alone

- Posture (`posture.go`) genuinely works — it reads VAC's own store/config, all in-process, no host
  dependency. Its findings already carry a proper `severity` (`ok`/`warn`/`error`,
  `posture.go:32`). P5.4's badge derives straight from these; no posture changes needed.
- The traffic *monitor* logic (`monitor.go`) is sound — sliding windows, LRU eviction, anomaly
  ring, notifier hook. It just has nothing to count when the access log isn't JSON. Don't touch the
  counters; fix the *signal* about why they're empty.
- The Caddy `/metrics` scrape (`scraper.go`) feeds host stats, not the security traffic panel; it's
  a separate path and out of scope for the "traffic feels broken" complaint.

---

## P5.1 — Honest status states (never a bare "failing") · Effort S–M

### What's wrong

`Fail2banState`/`FirewallState` carry a single `Detected bool` (`host.go:18-38`). The UI collapses
everything that isn't `detected` into one **"Not detected — not installed or readable on this
host"** empty state (`security-page.tsx:228-232,270-274`). Given reality-check **B**, that string is
actively misleading: it implies "you haven't installed fail2ban," when the truth is "VAC is
sandboxed and structurally can't see it." The operator *has* fail2ban installed, sees "not
detected," and concludes the feature is broken.

### Backend — replace the bool with a typed status

In `host.go`, add a status string to both states (keep `Detected` for one release if you want a
soft migration, or drop it — the UI is the only consumer). Proposed enum (lowercase tokens, same
style as `Severity.MarshalJSON`):

- `"healthy"` — read succeeded, data present.
- `"unreadable"` — VAC cannot read it from the sandbox (binary absent **in the container**, or
  present-but-permission-denied). This is the default state today for fail2ban/ufw and **must not**
  be phrased as "not installed."
- `"not_installed"` — only reportable **once a read helper exists** (P5.2) and the helper confirms
  the host binary is genuinely absent. Until P5.2, the backend never emits this.
- `"disabled"` — reserved for readers that can be turned off by config (not used by host reads
  today; keep the token for symmetry with traffic in P5.3).

Concretely:

```go
type Fail2banState struct {
    Status   string         `json:"status"` // healthy | unreadable | not_installed
    Detected bool           `json:"detected"` // == Status == "healthy"; kept for compat
    Jails    []Fail2banJail `json:"jails"`
}
// same shape change for FirewallState (Status + Backend/Active/Rules)
```

- `Fail2ban`: when `!h.look(...)` **or** `h.run(...)` errors → `Status:"unreadable"` (today's
  container reality). On success → `Status:"healthy"`. The current code already takes the right
  branches (`host.go:75-83`); just set `Status` instead of leaving `Detected:false`.
- `Firewall`: same — neither ufw nor nft readable → `Status:"unreadable"` (`host.go:154`); a parse
  success → `Status:"healthy"`.
- Factor the "is this VAC running where it could ever read the host?" hint into a tiny helper so the
  message can say *why* (sandboxed). Optional: a package-level `runsInContainer()` (`/.dockerenv`
  exists, or `cfg` flag) so the `unreadable` message can read *"VAC is sandboxed in a container and
  can't reach the host's fail2ban socket"* vs a generic permission note. Keep it best-effort.

### Frontend — three distinct empty states, helper-aware copy

`ui/src/types/api.ts`: add `status: 'healthy' | 'unreadable' | 'not_installed' | 'disabled'` to
`Fail2banState` and `FirewallState` (keep `detected` optional for the transition).

`security-page.tsx` `Fail2banPanel` / `FirewallPanel`: branch on `status`, not `!detected`:

- `unreadable` → **not** "not detected." Show an informative `EmptyState`:
  *"VAC can't read fail2ban from inside its sandbox. Enable the read helper to populate this panel."*
  with a short "Learn how" link to the helper docs (P5.2). Use a neutral/info tone, **not** an error
  tone — this is expected, not a failure.
- `not_installed` → *"fail2ban isn't installed on this host."* (only appears after P5.2).
- `healthy` with zero jails/rules → the existing "running but no jails" / "no rules" copy
  (`security-page.tsx:233-234,285-287`).
- `healthy` with data → today's render, unchanged.

This is the heart of the acceptance criterion *"distinguish not-installed / not-readable
(needs-helper) / disabled / healthy — never a bare 'failing.'"*

### Tests

- `host_test.go`: extend the existing stubbed-runner cases — `look` returns false →
  `Status:"unreadable"`; `run` returns an error → `Status:"unreadable"`; `run` returns valid output
  → `Status:"healthy"` with parsed jails/rules. (The harness already injects `run`/`look`.)
- Handler test (`security_test.go`): `GET /api/security/fail2ban` and `/firewall` serialize
  `status`.
- UI: a render test that `status:"unreadable"` shows the helper-aware copy (info tone), not the
  "not installed" string.

### Acceptance

- On a box with fail2ban + ufw installed but no read helper, both panels say *"VAC can't read this
  from its sandbox — enable the read helper,"* not "not detected." After P5.2 with the helper on,
  they show real jails/rules; with the helper on **and** the host binary genuinely missing, they say
  "not installed." No panel ever shows a bare red "failing."

---

## P5.2 — Opt-in read-only privileged helper · Effort M (architectural decision required)

### Why it's not a one-liner

The source plan suggests *"a documented `sudoers` line for `fail2ban-client status` / `ufw
status`."* That assumes `vac-api` runs **on the host**. It doesn't — it's a container
(`USER vac:vac`, `api/Dockerfile:72`) whose image lacks the binaries entirely (reality-check **B**),
and a host `sudoers` entry is invisible to a process inside a container. So the helper has to bridge
the **container → host** boundary, read-only, opt-in. Three viable designs, with the tradeoff each
makes:

| Option | Mechanism | Pros | Cons |
|---|---|---|---|
| **A. Bind-mount binaries + sockets** | Mount host `fail2ban-client`/`ufw`/`nft` and `/var/run/fail2ban/fail2ban.sock` read-only into the container | No new component | Brittle (shared-lib deps, distro path drift); fail2ban socket still root-owned → needs the container to read a root socket; widens the mount surface |
| **B. Host-side read daemon over a unix socket** | A tiny binary installed by `install.sh` (systemd unit) that runs `fail2ban-client status` / `ufw status` and serves JSON on a unix socket bind-mounted into `vac-api` | Clean privilege boundary; read-only by construction; container stays minimal; matches "control plane sandboxed" invariant | New host component + install/upgrade story + a systemd unit to maintain |
| **C. One-shot privileged helper container** | Reuse the already-mounted Docker socket (`compose.prod.yaml:80`) to `docker run --rm --pid=host --network=host` a small image that has the tools, exec the `status` read, capture stdout | No new host daemon; reuses existing Docker access | Spawns a privileged container per read (latency + a real escalation surface); arguably *more* power than a sudoers line |

**Recommendation: Option B.** It's the only one that preserves the "control plane is deliberately
sandboxed and never mutates host state" invariant (CLAUDE.md) while giving a *narrow, read-only,
auditable* seam. The helper is `vac-host-helper` (or fold into the existing `vac` CLI shipped by
`scripts/`), shipped opt-in:

1. **Host daemon** (`scripts/` + a small Go/sh binary): exposes `GET fail2ban` / `GET firewall`
   over a unix socket at e.g. `/run/vac/host-helper.sock`. It runs **only** whitelisted read
   subcommands (`fail2ban-client status [<jail>]`, `ufw status verbose`, `nft list ruleset`) — never
   anything that mutates. Same defensive argv discipline as `runReadOnly` (`host.go:58-65`).
2. **install.sh**: an opt-in step (prompt or `--with-host-helper`) that installs the unit, creates
   the socket dir, and adds `vac-api`'s container UID to the socket's group. Document the exact
   privilege granted (read-only fail2ban/ufw status).
3. **compose.prod.yaml**: when enabled, bind-mount `/run/vac/host-helper.sock` into the container
   (read-only) under a known path; gate on a new `VAC_HOST_HELPER` (default off).
4. **`host.go`**: when `VAC_HOST_HELPER` is set and the socket is present, `Host.Fail2ban`/`Firewall`
   talk to the socket (HTTP-over-unix) instead of `exec.LookPath`. The helper's response carries
   enough to set the honest `status` from P5.1 — including the genuine `not_installed` when the host
   binary is absent (the helper *can* tell, since it runs on the host). Keep the in-container
   `exec` path as a fallback for the (rare) bare-metal install.
5. **config**: `VAC_HOST_HELPER bool` (default false) + `VAC_HOST_HELPER_SOCKET` path; same
   env-override pattern as the other security knobs (`config.go:354-376`).

### Scope guardrails

- **Read-only, forever in this cut.** The helper exposes *no* ban/unban/rule-edit verb — that's the
  explicitly-deferred "write access" from [`../upcoming/15`](../upcoming/15-security-dashboard.md).
  Keep the helper's command whitelist literally three status reads.
- **Opt-in, off by default.** A fresh install behaves exactly as today (panels show P5.1's
  "unreadable — enable the helper"). No one gets a new host daemon they didn't ask for.
- **This is the item flagged for graduation.** Per the track note, P5.2 is the heaviest piece and
  "could graduate to its own `../upcoming/` stub." If the install/systemd surface balloons, split
  it: land P5.1/P5.3/P5.4 now, promote P5.2 to a standalone upcoming plan. The decision gate is
  whether Option B's install story stays small.

### Tests

- Helper: unit-test the command whitelist (rejects anything but the three reads) and the JSON
  encoding of `fail2ban-client`/`ufw` output (reuse the parsers already in `host.go` —
  `parseFail2banJail`, `parseUFW`, `parseNFT` — by lifting them to a shared spot or duplicating the
  thin parse).
- `host.go`: a stubbed socket transport → `Fail2ban`/`Firewall` return `healthy` with parsed data;
  socket absent → falls back to `unreadable`; helper reports binary missing → `not_installed`.
- An integration smoke (tagged `integration`) is optional and host-dependent; keep the unit layer
  authoritative.

### Acceptance

- With `VAC_HOST_HELPER=true` and the unit installed on a host that has fail2ban + ufw, the panels
  populate with real jails/rules. With the helper on but a tool absent, the panel says "not
  installed." With the helper off (default), the panel shows P5.1's sandbox-aware "enable the helper"
  message. The control plane still never mutates host state.

---

## P5.3 — Request-metrics: say *why* it's empty · Effort S

### What's wrong

`SecurityTrafficHandler` returns an empty `Snapshot` with `200 OK` and **no signal** in two very
different situations (reality-check **A**): (1) monitor disabled (`t == nil` →
`security.go:45-51`), and (2) monitor on but the collector has parsed zero usable lines. The UI
renders both as a quiet "No traffic" empty state (`security-page.tsx:158-160`), so a misconfigured
Caddy access log looks identical to a genuinely idle box.

### Backend — surface monitor + access-log health

Extend the traffic response so the UI can tell the three cases apart:

- **disabled** — `VAC_SECURITY_MONITOR=false`. The handler already knows (`t == nil`). Add
  `"monitoring": "disabled"` to the JSON.
- **idle** — monitor on, access log readable, no requests in the window.
- **no_signal** — monitor on, but the collector has never parsed a JSON line (Caddy JSON access
  logging not wired / wrong path / file unreadable). This is the high-value distinction per
  reality-check **A**.

Implementation:

1. **`reqmetrics/collector.go`**: track lightweight health — a `parsedLines` counter and
   `lastLineAt` (set in `handleLine` after a successful `json.Unmarshal`, `collector.go:138-149`),
   plus whether the log file opened (the tailer at `tailer.go` knows if the path is missing). Expose
   a `Stats()` returning `{LogPath, FileReadable bool, ParsedLines int, LastLineAt time.Time}`.
   Cheap, lock-guarded like the rest of the collector.
2. **`server/handler/security.go`**: give `SecurityTrafficHandler` access to the collector's
   `Stats()` (pass it in alongside the `SecurityTraffic` interface, or add a small
   `TrafficDiagnostics` interface). Compute `monitoring` from `t == nil`, and when enabled, derive
   `idle` vs `no_signal` from `ParsedLines > 0` / `FileReadable`. Wrap the snapshot:
   `{ "monitoring": "...", "log_readable": bool, "parsed_lines": n, ...existing Snapshot fields }`.
3. Keep the empty-snapshot fallback for `disabled` (`security.go:46-50`) so the shape is stable.

### Frontend — explain the empty panel

`ui/src/types/api.ts`: add `monitoring: 'enabled' | 'disabled'`, `log_readable: boolean`,
`parsed_lines: number` to `TrafficSnapshot`.

`security-page.tsx` `TrafficPanel`: above/instead of the bare stat strip, render a one-line banner
when something's off:

- `monitoring === 'disabled'` → *"Request monitoring is disabled. Set `VAC_SECURITY_MONITOR=true` to
  enable it."* (info tone). Still render the zeroed tiles dimmed.
- `monitoring === 'enabled'` && `!log_readable` → *"Caddy access log not found at the configured path
  — request metrics can't populate. Check `VAC_CADDY_ACCESS_LOG` and that Caddy JSON access logging
  is on."*
- `monitoring === 'enabled'` && `log_readable` && `parsed_lines === 0` → *"Access log is being read
  but no JSON request lines have been parsed yet — confirm Caddy's access log uses the JSON
  encoder."*
- otherwise (parsed_lines > 0, zero in window) → today's quiet "No traffic in the current window"
  (`security-page.tsx:159`) — now *correctly* meaning idle.

### Tests

- `reqmetrics_test.go`: feed the collector a JSON line → `Stats().ParsedLines == 1`,
  `LastLineAt` set; feed non-JSON → `ParsedLines` stays 0 (`collector.go:140` drops it). Missing log
  path → `FileReadable == false`.
- `security_test.go`: `t == nil` → `monitoring:"disabled"`; monitor present + `ParsedLines:0` →
  `monitoring:"enabled", parsed_lines:0`.
- UI: render test asserting each banner string for the three states.

### Acceptance

- A box with `VAC_SECURITY_MONITOR=false` shows "monitoring disabled — enable it," not a silent
  empty panel. A box where Caddy isn't writing JSON access logs shows the "no JSON lines parsed"
  hint. A genuinely idle box shows "no traffic in window." The three are visually distinct.

---

## P5.4 — Security nav badge (count of failing posture checks) · Effort S

### What to do

Posture findings already carry `severity` (`posture.go`); the page fetches them via
`useSecurityPosture()` (`lib/api/security.ts`). Derive a failing count client-side and badge the
Security nav item (`sidebar.tsx:22`) — no backend change, no new endpoint.

- **Count rule:** badge = number of findings with `severity === 'error'` (red). Optionally add an
  amber badge for `warn`-only when there are no errors. Don't count `ok` rows (they're passes).
  Note posture returns OK rows too (`posture.go:95-96` etc.), so filter, don't use `.length`.
- **Where:** `SidebarContent` (`sidebar.tsx:40-78`) maps `NAV`. Add an optional badge slot to the
  nav row render. Drive it from `useSecurityPosture()` — the query is already shared/cached (same
  key as the page, `query/keys.ts:46`), so mounting it in the sidebar costs one cached fetch, not a
  duplicate. Render a small count pill on the right of the "Security" row when `count > 0`.
- **Tone:** red pill for error count; if you also surface warns, use the existing degraded/amber
  token. Reuse a `Badge`/pill primitive from `components/ui` rather than hand-rolling.

> **Decision:** should the badge count P5.1's `unreadable` host panels as "failing"? **No** — an
> unreadable panel is a capability gap (helper not enabled), not a posture failure. Keep the badge
> strictly posture-`error` (plus optional posture-`warn`) so it reflects *actionable* problems, not
> "VAC is sandboxed."

### Tests

- UI: render `SidebarContent` with a mocked posture query returning 2 `error` + 3 `ok` findings →
  badge shows `2`; all-`ok` → no badge.

### Acceptance

- When posture has failing (`error`) checks, the Security nav item shows a red count badge; with a
  clean posture it shows none. Clicking through to the page shows the same failing rows.

---

## Cross-track sync points P5 touches

P5 is the most isolated track in the triage set — it shares **no files** with P1–P4 or P6:

- **App DTO** ([#1](00-parallel-tracks.md)) — untouched (P5 reads posture/host/traffic, not app
  DTOs).
- **`services.go`** ([#2](00-parallel-tracks.md)) — untouched.
- **App Settings UI** ([#3](00-parallel-tracks.md)) — untouched (P5 owns the Security page + the
  sidebar nav, distinct from app Settings).
- **Grafana addon template** ([#4](00-parallel-tracks.md)) — untouched.
- **Migrations** ([#5](00-parallel-tracks.md)) — **P5 adds none.**

The only file P5 shares with another *concern* is `sidebar.tsx` (P5.4 adds a badge slot) — but no
other triage track edits the sidebar, so there's no collision. Safe to hand wholesale to one agent.

## Suggested PR breakdown

1. `fix(security): honest fail2ban/firewall states instead of a bare "not detected"` — **P5.1**
   (typed `status` on host states + sandbox-aware UI copy). The highest-value honesty fix; explains
   the user's "everything's failing" feeling.
2. `fix(security): explain why the traffic panel is empty (disabled vs no-signal vs idle)` —
   **P5.3** (collector `Stats()` + handler `monitoring`/`log_readable` + UI banner).
3. `feat(security): badge the Security nav with the failing posture count` — **P5.4** (client-side
   derive from the existing posture query).
4. `feat(security): opt-in read-only host helper for fail2ban/firewall` — **P5.2** (host daemon +
   install/compose wiring + `host.go` socket path). Heaviest; may graduate to its own `upcoming/`
   stub — land it last or split it out.

PRs 1–3 are pure honesty/UX over data that already exists and carry near-zero risk; PR 4 is the one
real new capability and the one to gate behind opt-in.
