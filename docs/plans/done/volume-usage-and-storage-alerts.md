# Volume Usage & Storage Alerts — design sketch

Give a single-box operator an answer to "how much disk is each app eating, which volume is
growing, and tell me on Discord/Slack before it fills the box" — without SSHing in to run `du`.
Three layers, smallest-useful-first: **(1) read-only usage reporting → (2) soft per-app limits →
(3) threshold alerts through the existing notify subsystem.**

Status: **planned** (not started).

## Why

VAC already nudges "this service has a volume, configure a backup" (`services-tab.tsx` via
`has_volumes`), but it has **zero visibility into how full those volumes are**. On a single VPS —
especially a Raspberry Pi with a small SD card or an external HDD — a runaway Postgres or a
log-spewing app silently fills the disk, and the first symptom is the whole box wedging. The
host-wide disk total (`HostSnapshot.DiskUsedBytes`) shows *that* the disk is filling but not
*which app* is responsible.

This is a read-side feature plus one small monitor. The notify plumbing (Discord + Slack,
event toggles, SSRF guard, cooldown patterns) already exists — alerts are nearly free.

## Key technical realities (read before building)

- **`docker stats` `BlockIO` is NOT volume fill.** It's cumulative block-I/O *throughput* since
  container start (`dockercli/stats.go:23`, parsed but unused). It cannot answer "how full is the
  volume." Do not wire it in for this — it's a different metric and reusing it would be wrong.
- **Volume fill has two real data sources:**
  - **Named volumes** → `docker system df -v` reports each volume's size in one call.
  - **Bind mounts** (the external-HDD case) → `du -sb <hostpath>`. Accurate but potentially slow
    on large trees / spinning disks.
- **This is slow, periodic, persisted — not real-time.** Unlike CPU/RAM (subscriber-gated 2s WS
  stream in `stats/manager.go`), disk usage changes slowly and is expensive to sample. It belongs
  in a timer-driven background collector that writes to Postgres, mirroring the long-lived
  goroutine pattern of `certcheck`/`security`, **not** the WS stats manager.
- **Hard quota enforcement is deliberately out of scope.** Docker can only cap a volume's size
  (`--storage-opt size=`) on xfs+pquota / btrfs / zfs / devicemapper. On overlay2-over-ext4 — the
  default on a Raspberry Pi SD card or ext4 HDD — it does nothing. A "hard limit" would silently
  no-op on the most common VAC host. We do **soft limits** (monitor + alert), which work
  everywhere and promise nothing the filesystem can't keep.

## What already exists (don't rebuild)

- **Stateful-service flag**: `services.has_volumes` (migration `00065`), recomputed each deploy
  from `compose.ServicesWithVolumes` (`compose/preflight.go:158`). The set of services worth
  scanning is already known.
- **Notify subsystem**: `api/internal/notify/` — `Dispatcher`, `Event` model (`events.go:30`),
  Discord (`discord.go`) + Slack (`slack.go`) renderers, per-event JSONB toggle map, SSRF guard,
  fire-and-forget dispatch. Adding an alert = one event type + one toggle key + two ~10-line
  renderers.
- **Cooldown/dedup patterns to copy**: `security.Monitor` per-IP cooldown (`security/monitor.go`,
  default 10 min) and certcheck's "notified, clear on recovery" flag (`certcheck/certcheck.go`).
  Reuse one of these so a full volume doesn't fire every poll.
- **Per-app limit pattern**: `apps.mem_limit_mb` (nullable; `NULLIF(x,0)` clears) +
  `WriteResourceOverride` (`compose/wrap.go`). The soft disk limit reuses this column shape
  exactly — minus the compose override (soft limit is a threshold, not enforced at deploy).
- **Host disk vitals**: `HostSnapshot.DiskUsedBytes/DiskTotalBytes` (`stats/host.go`) already feed
  the dashboard — the host-level "disk 90% full" threshold can hang off the same numbers.
- **Prometheus per-app gauge pattern**: `vac_app_cpu_percent{app,service}` etc.
  (`promexport/promexport.go:98`) — mirror it for a disk gauge.
- **Backup disk-walk precedent**: the central-backup sketch already walks `{workDir}/backups` with
  `filepath.WalkDir` for `local_bytes`. Same idea for bind-mount sizing if we prefer Go-native
  over shelling out to `du`.

## Scope decisions (the important part)

1. **Read-only first, limits/alerts second.** Phase 1 ships value (visibility) with zero new
   config surface. Phases 2–3 are independently shippable.
2. **Soft limits only — never hard quotas.** Set a budget, monitor, alert. No `--storage-opt`, no
   false enforcement. (See realities above.)
3. **Threshold granularity — global default + per-app override.** A global default percent
   (e.g. alert at 85% of host disk) plus an optional per-app absolute `disk_limit_mb`. Matches the
   single-operator, low-config model; avoids a per-volume config explosion. Per-volume override is
   a later refinement if anyone asks.
4. **Bind-mount scanning is opt-in / bounded.** `du` on an external HDD can be slow. Default to
   scanning named volumes (cheap, from `docker system df -v`); bind-mount `du` runs on the same
   timer but is guarded (timeout + skip if the previous walk hasn't finished). Surface "not yet
   measured" rather than blocking the poll.
5. **Reporting is a periodic snapshot, not a live stream.** REST endpoint returning the latest
   persisted sample, not a WS topic. It changes on the order of minutes.

## Phase 1 — Backend collection + reporting

New package `api/internal/diskusage/`:

- `Collector` — long-lived goroutine on a timer (`VAC_DISK_POLL_INTERVAL`, default ~5 min),
  wired in `main.go` next to `certChecker`/`secMonitor`.
- Each tick:
  - One `docker system df -v` for all named volume sizes.
  - For services with `has_volumes`, `docker inspect` to map volume/bind mounts → owning app+service.
  - Bind mounts: bounded `du`/`WalkDir` (decision #4).
- Persist to a new table `volume_usage` (migration): `app_id`, `service_name`, `volume_name`,
  `mount_path`, `source` (`named|bind`), `used_bytes`, `sampled_at`. Keep latest per volume; a
  little history is nice for a sparkline but optional for v1.

Store: `UpsertVolumeUsage`, `ListVolumeUsageByApp(appID)`, and a box-wide
`ListVolumeUsage` for a future fleet view.

API: `GET /api/apps/{id}/volumes` →
```jsonc
{
  "volumes": [
    { "service": "db", "volume": "vac-blog_pgdata", "mount_path": "/var/lib/postgresql/data",
      "source": "named", "used_bytes": 824567321, "limit_bytes": 1073741824,
      "sampled_at": "2026-06-17T..." }
  ]
}
```
Handler in `server/handler/apps.go` (or a new `volumes.go`), REST snapshot, no WS.

Prometheus: add `vac_app_volume_bytes{app,service,volume}` to `promexport.go`, fed from the latest
persisted samples in the `/metrics` scrape path.

## Phase 2 — UI

- App-detail: a **Storage** section (new small tab, or a block on the overview / per service card).
  Per volume: name, mount path, size (`formatBytes`), and — if a limit is set — a fill bar
  (reuse the host-disk framing from `HostVitals`).
- Show `source` (named vs bind) and a "measured N min ago" timestamp so a stale/oversized bind
  mount that skipped a scan is honest about it.
- API client: `ui/src/lib/api/` add `volumes(appId)` + `useAppVolumes()` query.
  Types: add `VolumeUsage` to `ui/src/types/api.ts`.

## Phase 3 — Soft limits + alerting

- Limit storage: add `disk_limit_mb` to `apps` (mirror `mem_limit_mb`: nullable, `NULLIF(x,0)`
  clears). NULL = no limit = no per-app alert. Optionally a global default threshold percent in
  config (`VAC_DISK_ALERT_PERCENT`, default 85) for the host-level guard.
- Notify: new `EventDiskUsageHigh` in `notify/events.go`, a toggle key (`disk_usage_high`), and the
  two ~10-line renderers in `discord.go` / `slack.go` following the existing `Event` shape
  (Title "Storage high: blog/db", Message "82% of 1 GiB limit", `OK:false` → amber/red).
- Evaluation lives in the Phase-1 `Collector`: after each sample, compare against the per-app limit
  and/or host threshold; fire through the existing `notifier` with a **per-volume cooldown** copied
  from `security.Monitor`, and a "clear on recovery" flag like certcheck so it re-arms once usage
  drops. Two alert flavours: per-app/volume over its soft limit, and host disk over the global
  percent.

## Out of scope (explicitly)

- **Hard quota enforcement** (`--storage-opt size=`, filesystem quotas) — see realities.
- **Real-time per-volume streaming** — usage changes slowly; periodic snapshot only.
- **Volume management actions** — create / resize / migrate / prune. Read + alert only.
- **Per-volume limit config** — global default + per-app override first; per-volume later if asked.
- **Fleet-wide storage page** — the per-app view ships first; a central page can reuse
  `ListVolumeUsage` later (parallels the central-backup sketch).

## Rough size

- Phase 1: 1 package, 1 migration, ~3 store queries, 1 endpoint, 1 Prometheus gauge. Medium —
  the `docker system df -v` + `inspect` + bind-mount `du` plumbing is the real work.
- Phase 2: 1 section/tab, 1 query, 1 type. Small.
- Phase 3: 1 column, 1 event type + 2 renderers, threshold eval + cooldown in the collector. Small.

## Build order

1. `diskusage.Collector`: `docker system df -v` + `inspect` mapping; named volumes only.
2. `volume_usage` table + store methods; persist samples.
3. `GET /api/apps/{id}/volumes` + `vac_app_volume_bytes`.
4. UI Storage section + query + types.
5. Bind-mount `du` (bounded, opt-in) folded into the collector.
6. `disk_limit_mb` column + `EventDiskUsageHigh` + renderers + threshold eval with cooldown.
7. `/code-review` + `/simplify`; `/refresh-kb` if the module map changed (new `diskusage` package).
