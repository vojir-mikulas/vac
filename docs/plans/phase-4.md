# Phase 4 — Real-time

## Goal

Turn VAC from a request/response control plane into a **live** one. Phases 1–3 deploy apps,
route them through Caddy, and persist build logs, runtime logs (only crash-loop notices so far),
and request metrics to Postgres. Phase 4 streams that activity to clients as it happens and
pushes events out to the operator. By the end of Phase 4 you can:

1. `curl --include` an upgrade to `ws://…/api/deployments/:did/logs` while a deploy is running
   and watch build output arrive line-by-line, with the prior lines replayed on connect.
2. Connect to `ws://…/api/apps/:id/logs` and see every running container's stdout/stderr stream
   in, tagged by service — and have those same lines persisted to `runtime_logs` (capped at a
   per-service ring of 10k lines) whether or not anyone is watching.
3. Connect to `ws://…/api/apps/:id/stats` and get a per-service CPU / memory / network / uptime
   sample every 2s, sourced from `docker stats`, fanned out from a single collector.
4. Read host CPU / RAM / disk and the aggregate request rate (the Phase 3 scraper seam, now
   live) for the global dashboard.
5. Configure a Discord and/or Slack webhook and receive a message when a deploy succeeds or
   fails, when a service trips the crash-loop monitor, and when VAC itself restarts.

No dashboard rendering — that is Phase 5. Phase 4 produces the **real-time transport + producers
+ outbound notifications**, all still driven by a WebSocket client (`websocat`, browser console)
and `curl` against the settings API. The UI consumes these endpoints in Phase 5.

Reference: see `mvp.md` § Build Phases → Phase 4, § Real-time Stats Architecture, § Real-time
Logs Architecture, § Log Retention, § Notifications, and § API Surface → Real-time for original
scope. This document sequences that scope, picks the WebSocket library and hub design, resolves
the always-on vs. on-demand split for each producer, and defines exit criteria.

---

## Scope

### In

- **WebSocket hub** — an in-memory pub/sub `Hub` with string topics (`build:{deploymentID}`,
  `logs:{appID}`, `stats:{appID}`, `host`), bounded per-subscriber queues, and a slow-consumer
  drop policy. One hub, fan-out to N subscribers, producers publish without knowing subscribers.
- **WebSocket transport** — a single upgrade helper used by every WS endpoint: session/Bearer
  auth reuse, mandatory `Origin` check (anti-CSWSH), read-deadline ping/pong keepalive, and
  clean teardown on `ctx.Done()` or client close.
- **Live build logs** — the deploy pipeline's existing `LogWriter` additionally publishes each
  line to `build:{deploymentID}`; `WS /api/deployments/:did/logs` replays the DB backlog then
  attaches to the live topic, and closes when the deployment reaches a terminal status.
- **Runtime log capture + streaming** — a per-container `docker logs --follow` streamer that
  writes stdout/stderr to `runtime_logs` (DB) **and** publishes to `logs:{appID}`, plus a
  supervisor that tracks which containers are running (across deploy / start / stop / restart and
  on boot) and keeps exactly one follower per live container. `WS /api/apps/:id/logs` and
  `WS /api/apps/:id/services/:name/logs` replay recent DB lines then tail live.
- **Runtime-log ring buffer** — enforce the `mvp.md` per-service cap (10k lines) by trimming
  `runtime_logs` down to the newest N per `(app, service)`; complements (does not replace) the
  Phase 2 time-based prune.
- **Per-service stats streaming** — a per-app collector polling `docker stats --no-stream` every
  `VAC_STATS_POLL_INTERVAL` for the app's running containers, mapping container → service,
  publishing CPU % / memory MB / net rx-tx / uptime to `stats:{appID}`. **Subscriber-gated**:
  the collector only runs while a client is attached (stats are live-only, never persisted).
- **Host stats** — VPS CPU / RAM / disk via `gopsutil` (already an indirect dep) plus the live
  aggregate request rate from the Phase 3 `reqmetrics.Scraper` (the seam left wired in Phase 3),
  exposed as `GET /api/host/stats` (snapshot) and the `host` WS topic (push). Deferred from
  Phase 3's "Out" list.
- **Notifications** — a `notification_settings` singleton (Discord + Slack webhook URLs,
  encrypted at rest, per-event toggles) with env overrides (`VAC_NOTIFY_DISCORD_URL`,
  `VAC_NOTIFY_SLACK_URL`); a dispatcher that renders a Discord embed and a Slack Block Kit
  message and POSTs them with timeout + bounded retry; wired to **deploy succeeded**, **deploy
  failed**, **crash-loop detected**, and **VAC restarted**.
- Settings endpoints: `GET/PUT /api/settings/notifications`, `POST .../test` (fire a test ping).

### Out (deferred to later phases)

- Any dashboard / SPA rendering of logs, stats, or notification settings (Phase 5).
- **TLS-certificate-expiring** notification (`mvp.md` § Notifications, 14-day warning) — deferred;
  it needs reliable per-host cert-expiry data that Phase 3 only tracks as advisory `cert_status`.
  Recorded as a deviation (D7) with the plan to revisit once cert read-back exposes `not_after`.
- Custom / arbitrary-webhook channel (`mvp.md` marks it post-MVP).
- Persisting stats to the DB or a historical stats series — live-only for MVP (per
  `mvp.md` § Real-time Stats, which keeps no stats table).
- Activity-feed event table + its 30-day retention — the feed is a Phase 5 dashboard concern;
  Phase 4 only emits the outbound webhooks. (`activity_retention_days` stays unwired.)
- API-token (Bearer) auth over WebSocket from a browser — MVP WS auth is the session cookie;
  `Authorization` headers aren't settable on a browser `WebSocket`. Documented, not built.
- Back-pressure beyond drop-oldest / disconnect (no replay buffer, no durable per-client cursor
  for stats).

---

## Key technical decisions

### WebSocket library: `github.com/coder/websocket`

`coder/websocket` (the maintained successor to `nhooyr.io/websocket`) is context-first, has a
tiny API surface, zero transitive dependencies, and a built-in `Origin` allow-list — it matches
the codebase's existing preference for small, `context`-driven primitives (the Caddy client,
dockercli, the rate limiter all follow that shape). `gorilla/websocket` is the obvious
alternative but is callback/deadline-oriented and would need a hand-rolled origin check. We adopt
`coder/websocket`; `wsjson.Write` covers our JSON-frame needs without a codec layer.

### One hub, topic strings, bounded queues, drop-slow-consumers

The hub is a single process-wide `*ws.Hub` constructed in `main.go` and shared by every producer
and the WS handlers (mirrors how `proxyMgr` and `worker` are shared today). It is deliberately
**not** generic over message type — every frame is a pre-marshalled envelope
`{ "type": "...", "ts": ..., "service": ..., "data": {...} }`, so the hub moves `[]byte` and never
touches Go types of the producers.

- `Subscribe(topic) (<-chan []byte, cancel func())` returns a buffered channel (cap ~256).
- `Publish(topic, msg)` non-blocking sends to every subscriber; **if a subscriber's buffer is
  full it is dropped** (channel closed, subscriber observes it and the WS connection is closed
  with a "client too slow" code). A slow log viewer must never stall the pipeline or back up a
  `docker logs` follower.
- Topics are reference-counted so producers can ask `HasSubscribers(topic)` — this is what gates
  the stats collector (below). Log capture ignores it (always-on).

### Build-log live stream taps the existing `LogWriter`, no second pipeline

Build logs already flow `docker build → LogWriter → AppendDeploymentLogs` (`internal/deploy/
loggers.go`). Rather than re-stream docker output, we give `LogWriter` (and `LogSystem`) an
optional `Publisher` interface; each persisted row is also published to `build:{deploymentID}`.
The WS handler replays the DB backlog (`ListDeploymentLogs(after=0)`), records the highest id
seen, attaches to the live topic, and de-dups by id so a row written between "read backlog" and
"attach" isn't shown twice. When the deployment status is terminal
(`IsTerminalDeploymentStatus`), the handler drains remaining backlog and closes — a build log is
finite. This keeps the DB the source of truth and the live topic a pure tee.

### Runtime logs are always-on and DB-backed; stats are on-demand and ephemeral

`mvp.md` treats these asymmetrically and so do we:

- **Runtime logs** must persist (the ring buffer, the Logs Explorer, crash-loop forensics) — so
  the `docker logs --follow` followers run for every live container regardless of subscribers,
  writing to `runtime_logs` and publishing to `logs:{appID}` as a side effect. A subscriber that
  attaches mid-stream gets a DB backlog replay then the live tail.
- **Stats** are live-only (no table). Running `docker stats` continuously for every container on
  the host would burn CPU for data nobody is watching, so the per-app collector is **started on
  first subscribe and stopped on last unsubscribe** (`Hub.HasSubscribers`). A late subscriber
  gets the next 2s tick, no backlog (none exists).

### Runtime-log capture uses `docker logs --follow`, supervised against container churn

Each follower is `docker logs --follow --since=<ts> --timestamps <containerID>` streamed
line-by-line exactly like `dockercli.Events` / `runStreaming` already do — no Engine SDK. The
hard part is **container identity**: every redeploy gives a service a new container id, and
start/stop/restart change which containers exist. A `streams.Supervisor` owns the reconcile:

- On boot, on each successful deploy, and on each lifecycle action (start/stop/restart/delete),
  it lists the app's running containers (`docker compose ps`) and diffs against the followers it
  currently runs — starting a follower for new container ids, cancelling followers whose
  container is gone.
- It piggybacks on the **same `docker events` stream the crash-loop monitor already consumes**
  (`start` / `die` actions carry the compose project + service labels) so it reacts to container
  births/deaths without polling. (Extract a tiny fan-out so both the monitor and the supervisor
  read the one event stream rather than opening two.)
- Each follower is `ctx`-scoped; cancelling the supervisor (shutdown) cancels every follower.

`--since` is set to the follower's start time so a redeploy doesn't re-ingest the whole history;
`--timestamps` gives us the real container clock for the row `ts` rather than ingest time.

### Stats source: `docker stats --no-stream`, polled, mapped to services

`docker stats --no-stream --format "{{json .}}" <ids…>` returns one JSON object per container
(CPU %, mem usage/limit, net I/O, block I/O, PIDs) and exits — easy to parse, bounded, and
aligned to the 2s poll interval `mvp.md` specifies. We prefer this poll over the continuous
`docker stats` stream (whose default output is an ANSI-redrawn table and whose JSON stream still
emits a frame per container per ~1s, needing throttling). Uptime comes from the container's
`StartedAt` (one `docker inspect` per container, cached per follower lifetime). Container → service
mapping reuses the compose labels already on the `services` table (`container_id`).

### WebSocket auth = session cookie + mandatory Origin check

The WS upgrade is an HTTP `GET`, so it flows through the existing `Auth` middleware (cookie or
Bearer) and passes the `CSRF` middleware untouched (CSRF skips safe methods). We mount the WS
routes **inside the `RequireSession` group**, so an unauthenticated upgrade is rejected with 401
before the handshake. Because browsers attach cookies to cross-origin WS handshakes automatically
(classic cross-site WebSocket hijacking), the upgrade helper **must** verify `Origin` against
VAC's own host(s) — `coder/websocket`'s `AcceptOptions.OriginPatterns`, derived from
`VAC_BASE_DOMAIN` / the request host, with `InsecureSkipVerify` only in `VAC_EXPOSURE=local`
explicit-opt-in. This is the one genuinely new security surface in Phase 4 and is called out in
M1 and the exit criteria.

### Notifications: a small dispatcher, encrypted webhook URLs, fire-and-forget with retry

Webhook URLs are bearer-secret (anyone holding the URL can post to the channel) so they are
stored **encrypted with the existing `crypto.Box`**, exactly like env vars / SSH keys / TOTP
secrets — and the env overrides (`VAC_NOTIFY_*`) take precedence when set. Dispatch is
fire-and-forget from the event site: events are handed to a `notify.Dispatcher` which renders the
channel-specific payload and POSTs with a short timeout and a bounded retry (3×, backoff), never
blocking the pipeline / monitor / boot path. A failed webhook is logged, never fatal. Discord gets
a colour-coded embed; Slack gets a Block Kit message; both carry app name, commit SHA + message,
duration, status, and a deep link (`{public base}/apps/{id}` — best-effort from `VAC_BASE_DOMAIN`).

---

## Library decisions

| Concern | Pick | Why |
|---|---|---|
| WebSocket server | `github.com/coder/websocket` | Context-first, zero-dep, built-in `OriginPatterns`; `wsjson` helper for JSON frames. Matches the codebase's small-primitive style. |
| JSON frames | `coder/websocket/wsjson` + stdlib `encoding/json` | Envelopes are tiny structs we own. No codec layer. |
| Host stats | `github.com/shirou/gopsutil/v4` | **Already an indirect dependency** (via testcontainers) — promote to direct. Cross-platform CPU/mem/disk without shelling out. |
| Container stats | `docker stats --no-stream` via `os/exec` (dockercli) | Matches the CLI-only approach (Phase 2/3 shell out, no Engine SDK). One bounded call per poll. |
| Runtime log follow | `docker logs --follow` via `os/exec` (dockercli) | Same streaming pattern as `dockercli.Events`/`runStreaming`; zero new deps. |
| Caddy request-rate scrape | `reqmetrics.Scraper` (exists) | Phase 3 already built and unit-tested `SumCounter`/`TotalRequests`; Phase 4 just calls it on the host-stats path. |
| Webhook HTTP | stdlib `net/http` | Two simple JSON POSTs; no SDK warranted. |

**Not adopting:**

- `gorilla/websocket` — heavier API, no built-in origin allow-list (see decision above).
- A message broker / external pub-sub — the hub is in-process; VAC is single-node by design.
- A stats time-series store (Prometheus, a `stats` table) — live-only for MVP per `mvp.md`.
- The Docker Engine SDK for logs/stats — stays off the hot path, consistent with `dockercli`'s
  package doc.

---

## File layout (additions in Phase 4)

```
api/
├── go.mod                                   # + coder/websocket; gopsutil promoted to direct
├── internal/
│   ├── ws/
│   │   ├── hub.go                           # Hub: Subscribe/Publish/HasSubscribers, bounded queues, drop-slow
│   │   ├── conn.go                          # Accept(): upgrade + Origin check + auth assert + ping/pong + pump
│   │   ├── envelope.go                       # frame envelope {type, ts, service, data}
│   │   └── hub_test.go
│   ├── logstream/
│   │   ├── follower.go                       # one `docker logs --follow` → DB + hub, ctx-scoped
│   │   ├── supervisor.go                      # reconcile followers against running containers + events
│   │   └── logstream_test.go
│   ├── stats/
│   │   ├── collector.go                       # per-app `docker stats` poll → hub, subscriber-gated
│   │   ├── host.go                            # gopsutil host snapshot + caddy scrape
│   │   ├── parse.go                           # docker stats json → typed sample (CPU%, mem, net, uptime)
│   │   └── stats_test.go
│   ├── notify/
│   │   ├── dispatcher.go                       # event → render → POST with retry/timeout
│   │   ├── discord.go                          # embed payload
│   │   ├── slack.go                            # Block Kit payload
│   │   ├── events.go                           # event types + render-neutral payload struct
│   │   └── notify_test.go
│   ├── store/
│   │   ├── notification_settings.go            # singleton get/put (encrypted URLs)
│   │   └── runtime_logs.go                     # + TrimRuntimeLogsToRingBuffer(app, service, keepN)
│   ├── db/migrations/
│   │   └── 00015_notification_settings.sql
│   └── server/handler/
│       ├── ws_logs.go                          # WS build / runtime log handlers
│       ├── ws_stats.go                         # WS per-app stats handler
│       ├── host_stats.go                       # GET /api/host/stats + WS host topic
│       └── notifications.go                    # GET/PUT settings, POST test
```

`internal/ws` is a dumb transport + fan-out that knows nothing about apps. `internal/logstream`
and `internal/stats` are producers that depend on `dockercli`, `store`, and the hub.
`internal/notify` depends only on `store`, config, and `net/http`. Handlers stay thin — they
subscribe to a topic, replay a DB backlog where relevant, and pump frames — matching the Phase 2/3
handler convention. The crash-loop monitor and pipeline gain a `Notifier` field (an interface
they call on the relevant transitions), wired to `notify.Dispatcher` in `main.go`.

---

## Data model additions

### `notification_settings` (00015)

A singleton row (single-tenant control plane) holding the channel config. Webhook URLs are
encrypted with `crypto.Box` before storage (`bytea`), mirroring `env_vars` / `ssh_keys`.

```
id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1)   -- enforce one row
discord_url_enc BYTEA                                            -- AEAD ciphertext, NULL = unset
slack_url_enc   BYTEA
events          JSONB NOT NULL DEFAULT '{}'::jsonb               -- per-event enable map
updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
```

`events` is a small map (`{"deploy_succeeded":true,"deploy_failed":true,"crash_loop":true,
"vac_restarted":true}`); an absent key defaults to **on** for the four implemented events. Env
overrides (`VAC_NOTIFY_DISCORD_URL` / `VAC_NOTIFY_SLACK_URL`) are read at dispatch time and win
over the stored value, so a scripted deploy needs no DB write. Store accessors:
`GetNotificationSettings`, `PutNotificationSettings` (encrypt on write, decrypt on read; when
`Box` is nil, URLs cannot be stored — return a clear error, same posture as TOTP setup).

### `runtime_logs` — no schema change, new trim accessor

The table (00009) already exists and is time-pruned to 7 days by the Phase 2 retention goroutine.
Phase 4 adds the per-service **ring buffer** cap from `mvp.md` (10k lines per service) via a new
store method, not a migration:

```go
// TrimRuntimeLogsToRingBuffer keeps only the newest keepN rows per (app, service),
// deleting older overflow. Called by the follower periodically and by the nightly pruner.
DELETE FROM runtime_logs
WHERE id IN (
  SELECT id FROM (
    SELECT id, row_number() OVER (PARTITION BY app_id, service_name ORDER BY id DESC) AS rn
    FROM runtime_logs WHERE app_id = $1 AND service_name = $2
  ) t WHERE t.rn > $3
);
```

No stats table, no activity-feed table (both out of scope — see Scope/Out).

---

## Sequence

### M1 — WebSocket hub + transport

**Goal:** a shared in-memory hub and a single, secure upgrade path that every WS endpoint reuses.

- `internal/ws/hub.go`: `Hub` with `Subscribe(topic) (ch <-chan []byte, cancel func())`,
  `Publish(topic string, msg []byte)`, `HasSubscribers(topic) bool`, `OnSubscribe/OnUnsubscribe`
  callbacks (used by the stats collector gate). Per-subscriber buffered channel (cap 256);
  full buffer → close the channel (drop the slow consumer). Mutex-guarded `map[topic]map[*sub]`;
  reference-count topics so empty topics are GC'd.
- `internal/ws/envelope.go`: `Frame{Type string; TS time.Time; Service string `,omitempty`;
  Data json.RawMessage}` + a `Marshal` helper producers call once.
- `internal/ws/conn.go`: `Accept(w, r, opts) (*Conn, error)` wrapping `websocket.Accept` with
  `OriginPatterns` derived from config (M’s host + `VAC_BASE_DOMAIN`); a `Serve(ctx, ch)` pump
  that writes frames, runs a ping/pong keepalive, and returns on client close or `ctx.Done()`.
  Reject the upgrade if `auth.User(ctx) == nil` (defence in depth behind `RequireSession`).
- `main.go`: construct `hub := ws.NewHub()`, pass into `server.New` and into the producers built
  in later milestones.

**Test:** unit — `Subscribe`/`Publish` fan-out to multiple subscribers; a full-buffer subscriber
is dropped without blocking `Publish`; `HasSubscribers` flips with the last unsubscribe;
ref-count GC. Transport — `httptest` server with `coder/websocket` client: a same-origin upgrade
succeeds, a foreign `Origin` is rejected, an unauthenticated upgrade is 401.

### M2 — Live build logs

**Goal:** build output streams to WS clients while persisting unchanged.

- `internal/deploy/loggers.go`: add an optional `Publisher interface { Publish(topic string, b
  []byte) }` and `appID`/`deploymentID` topic to `LogWriter` + `LogSystem`. After a successful
  `AppendDeploymentLogs`, publish each row as a `build` frame to `build:{deploymentID}`. Nil
  publisher → today's behaviour (tests, no-hub).
- Wire the hub as that publisher when the pipeline is constructed in `main.go`.
- `internal/server/handler/ws_logs.go`: `BuildLogsWS(s, hub)` for `WS /api/deployments/:did/logs`
  — authorise the deployment belongs to a known app, replay `ListDeploymentLogs(did, after=0)`,
  track max id, `Subscribe(build:{did})`, de-dup by id, pump; if the deployment is already
  terminal, replay and close; otherwise close when a terminal-status frame/notice arrives.
- Route: inside the `RequireSession` group, add `r.Get("/deployments/{did}/logs", …)` (a new
  top-level `/api/deployments` route alongside the per-app one, matching `mvp.md` § API surface).

**Test:** unit — `LogWriter` with a fake publisher emits one frame per persisted row and still
calls the sink. Integration — start a deploy against a fixture, connect mid-build, assert backlog
replay + live lines + no duplicates + socket closes at terminal status.

### M3 — Runtime log capture + streaming + ring buffer

**Goal:** every running container's logs persist and stream, surviving redeploys.

- `internal/logstream/follower.go`: `Follower` runs `docker logs --follow --timestamps
  --since=<start> <cid>` (new `dockercli.Logs(ctx, cid, since) (<-chan LogLine, error)` modelled
  on `Events`), classifies stdout/stderr, batches into `AppendRuntimeLogs`, and publishes each
  line as a `log` frame to `logs:{appID}` (tagged service). Periodically calls
  `TrimRuntimeLogsToRingBuffer`. Stops on `ctx.Done()`.
- `internal/logstream/supervisor.go`: `Supervisor` keeps `map[containerID]*Follower`. `Reconcile
  (ctx, appID)` diffs running containers (`compose ps` / `services.container_id`) against current
  followers. Subscribes to the shared docker-events fan-out to react to `start`/`die`. `BootSync`
  reconciles every app on startup.
- Refactor the crash-loop monitor's single `docker events` consumer into a small shared
  `dockercli` fan-out (`events.Bus`) so the monitor **and** the supervisor read one stream.
- `internal/store/runtime_logs.go`: add `TrimRuntimeLogsToRingBuffer(ctx, appID, service,
  keepN)`. Pruner (`internal/retention`) also calls it for all `(app, service)` pairs nightly.
- Handlers: `RuntimeLogsWS` for `WS /api/apps/:id/logs` and
  `WS /api/apps/:id/services/:name/logs` — replay `ListRuntimeLogs` (recent N, optional service
  filter), then attach to `logs:{appID}` (filter by service for the per-service route), pump.
- Pipeline / lifecycle handlers call `supervisor.Reconcile(appID)` after deploy / start / stop /
  restart; `main.go` runs `BootSync` and starts the supervisor goroutine under `ctx`.

**Test:** unit — feed synthetic `docker logs` lines through the follower, assert DB batch +
published frames + stream classification; supervisor diff starts/stops the right followers for an
add/remove. Integration — deploy a fixture that prints to stdout, connect to the app logs WS,
assert lines stream and land in `runtime_logs`; redeploy and assert the follower re-attaches to
the new container without duplicating history; assert ring-buffer trim caps a noisy service.

### M4 — Per-service stats + host stats

**Goal:** live CPU/mem/net/uptime per service, plus host vitals, on demand.

- `dockercli`: add `Stats(ctx, ids []string) ([]StatSample, error)` running `docker stats
  --no-stream --format "{{json .}}" <ids…>`; `parse.go` converts the human strings ("12.3MiB",
  "1.2kB / 3.4kB", "5.00%") into typed numbers.
- `internal/stats/collector.go`: per-app `Collector` that, while `hub.HasSubscribers(stats:{app})`,
  ticks every `VAC_STATS_POLL_INTERVAL`, lists the app's running container ids, calls `Stats`,
  maps container → service, computes uptime from cached `StartedAt`, and publishes a `stats`
  frame per service. Started by the hub's `OnSubscribe(stats:{app})`, stopped on last
  unsubscribe — no work when nobody watches.
- `internal/stats/host.go`: `HostSnapshot(ctx)` via `gopsutil` (CPU %, mem used/total, disk
  used/total for the data volume) + `reqmetrics.Scraper.TotalRequests` delta for aggregate
  request rate. Promote `gopsutil/v4` to a direct dependency.
- Handlers: `StatsWS` for `WS /api/apps/:id/stats`; `HostStats` for `GET /api/host/stats`
  (snapshot) and a `host` WS topic driven by a single always-cheap host ticker (gated on
  subscribers too).

**Test:** unit — `parse.go` table-driven over real `docker stats` JSON shapes (units, ranges,
empty net); collector starts only when a subscriber exists and stops on unsubscribe (fake hub +
fake docker). Integration — deploy a fixture, subscribe to stats, assert ≥1 sample per running
service with plausible CPU/mem; host snapshot returns non-zero mem/disk.

### M5 — Notifications

**Goal:** outbound Discord/Slack on deploy, crash-loop, and restart events.

- Migration `00015_notification_settings.sql` + `internal/store/notification_settings.go`
  (encrypted URL get/put, `events` JSONB).
- `internal/notify`: `Event` types + a render-neutral payload (app, status, commit, duration,
  url); `discord.go` (embed) and `slack.go` (Block Kit); `dispatcher.go` —
  `Dispatch(ctx, ev)` reads settings (env override > DB), checks the per-event toggle, renders to
  each configured channel, POSTs with a 5s timeout and 3× backoff retry, logs failures. Runs the
  POST on a detached goroutine so callers never block.
- Wire the producers:
  - Pipeline: on `MarkDeploymentFinished(Running)` → `deploy_succeeded`; on the `error`/`degraded`
    finish paths → `deploy_failed` (carry commit SHA/message + duration).
  - Crash-loop monitor (`trip`): → `crash_loop` (service, restart count, exit code).
  - `main.go` boot: after the server is listening, → `vac_restarted` (skip on genuine first boot
    — gate on `CountUsers > 0` so a brand-new install doesn't ping).
- Handlers (`internal/server/handler/notifications.go`, in `RequireSession`):
  `GET /api/settings/notifications` (redacts URLs to a boolean "configured" + last 4 chars),
  `PUT /api/settings/notifications`, `POST /api/settings/notifications/test` (dispatch a synthetic
  ping to verify the webhook).

**Test:** unit — Discord/Slack renderers produce valid payloads for each event (golden JSON);
dispatcher honours the per-event toggle, env-override precedence, retry on 5xx, and gives up after
N; the `GET` handler never leaks the full URL. Integration — `PUT` a settings row (encrypted),
`GET` shows "configured", `POST .../test` hits an `httptest` webhook capturing the body.

### M6 — Hardening pass

- **Graceful shutdown:** on `ctx` cancel, the hub stops accepting, all WS conns close with a going-
  away code, followers and collectors exit, in-flight notification POSTs are bounded by their own
  timeout. Verify no goroutine leak (a `goleak`-style check or a follower/subscriber count back to
  zero after cancel).
- **Origin / auth:** confirm a cross-origin WS handshake is rejected and an unauthenticated one is
  401; confirm `VAC_EXPOSURE=local` is the only way to relax origin checking, and that it’s
  explicit.
- **Slow-consumer safety:** a deliberately stalled subscriber is dropped without backing up the
  pipeline / a follower / the stats collector (assert publish latency stays bounded).
- **Resource bounds:** stats collector and host ticker do nothing with zero subscribers (assert no
  `docker stats` invocations when unsubscribed); followers cap memory via batched inserts + ring
  trim.
- **RAM:** control plane (vac-api + vac-proxy, excluding Postgres + user containers) still idles
  under 200 MB with a handful of followers running; note the per-follower `docker logs` process
  cost in the PR.
- `golangci-lint run ./...` clean.
- Manual end-to-end on a real VPS: deploy a multi-service app, watch build logs stream, watch
  runtime logs + per-service stats stream, redeploy and confirm followers re-attach, trip a
  crash-loop and confirm both the WS `system` line and the Discord/Slack message, restart vac-api
  and confirm the "VAC restarted" notification.

---

## Testing strategy

| Layer | Tool | What it covers |
|---|---|---|
| Unit | `go test` | hub fan-out + drop-slow + ref-count; envelope marshalling; `docker stats` JSON parse; log-line classification; notification renderers + dispatcher retry/toggle/precedence; ring-buffer trim SQL |
| Transport | `httptest` + `coder/websocket` client | upgrade success same-origin, reject foreign origin, 401 unauthenticated, ping/pong keepalive, clean close |
| Handler | `httptest` + chi | build/runtime/stats WS replay + live shape; notification settings GET redaction, PUT, test-ping |
| Integration (DB) | `testcontainers-go` Postgres | notification_settings encrypted round-trip; runtime_logs ring-buffer trim keeps newest N |
| Integration (Docker) | host daemon, `t.Skipf` if absent | follower captures real `docker logs`, survives redeploy (new container id), stats sampled per service, supervisor reconcile on lifecycle |
| Integration (webhook) | `httptest` capturing server | dispatcher posts the rendered body for each event with retry |
| Manual / real VPS | `websocat` + curl + a Discord/Slack webhook | end-to-end live logs/stats, crash-loop + restart notifications |

**Determinism:** the hub and supervisor are driven by injected clocks/fakes where they touch time
or docker; integration tiers `t.Skipf` when the docker daemon is unavailable, exactly as Phase 3
does. Notification tests never hit real Discord/Slack — only an `httptest` sink.

---

## Configuration additions

These are documented in `mvp.md` § Configuration and become live in Phase 4:

| Variable | Default | First used by |
|---|---|---|
| `VAC_STATS_POLL_INTERVAL` | `2s` | M4 stats collector tick |
| `VAC_LOG_RING_BUFFER` | `10000` | M3 per-service ring-buffer trim |
| `VAC_NOTIFY_DISCORD_URL` | `` | M5 dispatcher (env override of stored URL) |
| `VAC_NOTIFY_SLACK_URL` | `` | M5 dispatcher (env override of stored URL) |

Extend `internal/config/config.go` with `StatsPollInterval time.Duration` (`stats.poll_interval`),
`LogRingBuffer int` (`logs.ring_buffer_lines` / `VAC_LOG_RING_BUFFER`), and
`NotifyDiscordURL` / `NotifySlackURL` (`yaml:"-"`, env-only — they're semi-secret, never in the
file). All four already appear in `mvp.md` § Configuration; this is their first live wiring. The
existing `CaddyMetricsInterval` is reused for the host request-rate scrape; no new ACME/Caddy
keys. WS origin allow-listing derives from the existing `BaseDomain` + request host and
`Exposure` (no new key).

---

## Exit criteria

Phase 4 is done when all of these pass on a fresh clone:

- [ ] `WS /api/deployments/:did/logs` replays the persisted build log then streams new lines live
      during a deploy, with no duplicates, and closes when the deployment reaches a terminal status
- [ ] `WS /api/apps/:id/logs` streams every running container's stdout/stderr tagged by service;
      the same lines are persisted to `runtime_logs`; `WS /api/apps/:id/services/:name/logs`
      filters to one service
- [ ] Runtime-log capture runs with **no** WS subscriber attached (DB still fills), and a service
      that floods stdout is capped to the newest `VAC_LOG_RING_BUFFER` lines per service
- [ ] A redeploy re-attaches the log follower to the new container id without re-ingesting history
      or leaking the old follower
- [ ] `WS /api/apps/:id/stats` delivers a per-service CPU / memory / network / uptime sample every
      `VAC_STATS_POLL_INTERVAL`; the collector runs **only** while a subscriber is attached (no
      `docker stats` calls when nobody is watching)
- [ ] `GET /api/host/stats` returns host CPU / RAM / disk and an aggregate request rate; the `host`
      WS topic pushes the same
- [ ] A WebSocket upgrade from a foreign `Origin` is rejected; an unauthenticated upgrade is 401;
      origin checking can be relaxed only via explicit `VAC_EXPOSURE=local`
- [ ] A slow/stalled WS subscriber is dropped without stalling the pipeline, a log follower, or the
      stats collector
- [ ] `PUT /api/settings/notifications` stores Discord/Slack webhook URLs **encrypted**; `GET`
      never returns the full URL; `POST .../test` delivers a ping to the configured webhook
- [ ] A successful deploy, a failed deploy, a crash-loop trip, and a vac-api restart each dispatch
      a Discord embed and/or Slack Block Kit message (per the per-event toggles); env
      `VAC_NOTIFY_*` overrides the stored URL; a webhook failure is logged, never fatal, and never
      blocks the triggering path
- [ ] On shutdown the hub closes every connection, all followers and collectors exit, and no
      goroutines leak
- [ ] `golangci-lint run ./...` is clean
- [ ] Integration suite passes locally (Postgres + Docker tiers; webhook via httptest)
- [ ] Control plane still idles under 200 MB RAM (vac-api + vac-proxy, excluding Postgres and user
      containers) with several log followers active; note the per-follower cost in the PR
