# Storage Overview — aggregated app totals + a fleet-wide Storage page

Follow-up to [`volume-usage-and-storage-alerts.md`](volume-usage-and-storage-alerts.md), which
landed the collector, per-mount persistence, per-app `GET /api/apps/{id}/volumes`, soft limits,
and alerts. That gave us **per-volume** numbers. This adds the three things a single-box operator
actually asks next:

1. **An aggregated total per app** — "how much disk is this app eating," at a glance.
2. **That total in the app's right-column overview panel** — visible without scrolling to Storage.
3. **A fleet-wide Storage page** — host disk breakdown + "which app/volume takes the most space" +
   reclaim button.

Status: **planned** (not started).

Smallest-useful-first: items 1–2 are pure UI over data already on the client; item 3 adds one thin
read endpoint + one page. Ship 1–2 first, then 3.

## Why

The data is collected, persisted, and exposed per-app — but the UI only renders **per-volume rows**.
An app with three volumes and no configured limit shows three sizes and *no total*
(`storage-section.tsx:32` only renders the summary when a limit is set). And there's no box-wide
answer to "what's filling the disk" short of reading every app's Storage card one by one. The
backend already anticipated this: `store/volume_usage.go:80` calls out "a future fleet-wide storage
view," and the box-wide `ListVolumeUsage()` query + `GET /api/instance/disk` (docker system df) +
`POST /api/instance/prune` all already exist.

## What already exists (don't rebuild)

- **Per-app volume data on the client**: `useAppVolumes(appId)` → `GET /api/apps/{id}/volumes`
  returns `VolumeUsage[]` with `used_bytes` (nullable) and `limit_bytes` echoed per row
  (`ui/src/lib/api/apps.ts:51`, `ui/src/types/api.ts:70`).
- **The total is already computed** in `StorageSection` (`storage-section.tsx:25`) — it's just only
  shown inside the budget meter.
- **Box-wide query**: `store.ListVolumeUsage()` (`store/volume_usage.go:81`) returns every sample
  joined to app slug. Currently only consumed by the Prometheus exporter.
- **Host disk breakdown**: `GET /api/instance/disk` → `DiskUsage{images,containers,volumes,build_cache}`
  with reclaimable bytes (`handler/disk.go:29`, hook `useDiskUsage()` `lib/api/instance.ts:112`).
- **Reclaim**: `POST /api/instance/prune` + `usePruneDisk()` (`handler/disk.go:48`), already wired
  into Settings → Maintenance (`features/settings/maintenance-section.tsx`).
- **Host disk vitals**: `useHostStats()` exposes `disk_used_bytes/disk_total_bytes`, already drawn in
  the sidebar `HostVitals` (`components/layout/sidebar.tsx:195`).
- **Page/route pattern**: file route `routes/_app/<name>.tsx` → `createFileRoute('/_app/<name>')`
  rendering a `features/<name>/<name>-page.tsx` (see `database.tsx`). `routeTree.gen.ts` is generated.
- **Sidebar nav**: literal `NAV` array (`sidebar.tsx:26`); managed-services items are spliced in.
- **`formatBytes` / `relativeTime`** (`lib/format`) and the `Meter` component for fill bars.

## A subtlety to handle honestly: unmeasured mounts

`used_bytes` is `null` for bind mounts when `DiskScanBinds` is off (the default — `du` is slow on
spinning disks). The existing per-volume row renders "not measured" rather than a false 0. An
aggregated total must **sum only measured bytes** and not pretend the null mounts are 0. Decision:
sum measured bytes; when any mount in the aggregate is unmeasured, suffix a quiet
"+N not measured" hint so the number reads as a floor, not a lie. Same rule for the per-app total,
the overview-panel row, and the fleet page rows.

---

## Part 1 — Aggregated total in the app Storage card (UI only)

`storage-section.tsx`. Always render a total in/near the section header, independent of whether a
limit is set:

- **Limit set** → keep the existing meter (`formatBytes(total) / formatBytes(limit)` + `Meter`).
- **No limit** → show `formatBytes(total)` alone (no meter, no fake percentage).
- Append the "+N not measured" hint when any row has `used_bytes == null`.

No API/backend/i18n-key churn beyond one or two new strings. ~10 lines.

## Part 2 — Storage total in the right-column overview panel (UI only)

`overview-panel.tsx`, the "Stack" card (services / RAM cap / network). Add a **Storage** `Row`:

- Call `useAppVolumes(appId)`, reuse the same measured-sum + not-measured logic (extract a tiny
  `sumVolumes(volumes)` helper into `lib/format` or a local util so Parts 1–2 share it — avoid two
  copies of the null-handling).
- Render the row **only when the app has volumes** (skip for stateless apps, matching how
  `StorageSection` returns null on empty), so it doesn't add a "0 B" line to every web app.
- Value: `formatBytes(total)`, with the not-measured hint as a muted suffix if applicable.

~15 lines + one shared helper.

## Part 3 — Fleet-wide Storage page

The real feature. New top-level page at `/storage`, reachable from the sidebar, answering "what is
filling this box."

### 3a. Backend — one thin read endpoint

`GET /api/instance/storage` → aggregate `store.ListVolumeUsage()` by app:

```go
type AppStorage struct {
    Slug          string  `json:"slug"`
    Name          string  `json:"name"`        // if cheap to join; else slug only
    UsedBytes     int64   `json:"used_bytes"`  // sum of measured mounts
    VolumeCount   int     `json:"volume_count"`
    UnmeasuredCount int   `json:"unmeasured_count"`
    LimitBytes    *int64  `json:"limit_bytes"` // disk_limit_mb*MiB, nil = no limit
}
type StorageResponse struct {
    Apps    []AppStorage  `json:"apps"`         // sorted by used_bytes desc
    Host    DiskUsage     `json:"host"`         // reuse SystemDF breakdown
}
```

- New handler `handler/InstanceStorage(store, diskReporter)` — aggregates in Go from the existing
  box-wide query; fold the existing `SystemDF` call in so the page is one request. Register in
  `server.go` next to the other `instance/*` routes (note `instance/disk`/`instance/prune` are
  registered somewhere other than the grep'd line — locate and mirror).
- No migration, no new store method (reuse `ListVolumeUsage`; optionally add a small
  `disk_limit_mb`/name join, or aggregate in Go and look up limits from the apps the collector
  already knows). Prefer **aggregate-in-Go** to keep the store query untouched.

### 3b. Frontend — the page

- `lib/api/instance.ts`: `instanceApi.storage()` + `useInstanceStorage()` (staleTime ~60s, matching
  `useDiskUsage`). Add a `queryKeys.instance.storage` key.
- `routes/_app/storage.tsx` + `features/storage/storage-page.tsx`.
- Sidebar: add `{ to: '/storage', label: 'Storage', icon: HardDrive }` to `NAV` (sensible slot:
  after Database). `HardDrive` is already imported elsewhere; import in sidebar.
- Page layout:
  - **Host disk** — total used/total + breakdown bar (images / volumes / build cache / containers)
    from the `host` block; reuse the `maintenance-section` rendering or factor a shared `UsageRow`.
  - **Apps by storage** — table/list sorted desc by `used_bytes`: app name, total, volume count,
    budget meter when `limit_bytes` set, "+N not measured" hint. Rows link to the app's Storage
    section (`/apps/$slug` → Services tab anchor, or deep-link if cheap).
  - **Reclaim** — a "Reclaim space" button wired to `usePruneDisk()` (reuse existing mutation +
    toast), showing reclaimable bytes from the host breakdown. Keep it identical in behavior to the
    Settings → Maintenance prune so there's one code path (consider importing the existing button).
- i18n: new `storage` namespace (or extend an existing one) for page copy; type-checked keys like the
  existing features.

### 3c. Out of scope (call out, don't build)

- **Per-app history / growth charts** — `volume_usage` holds only the latest sample per mount
  (upsert, not append). Trend lines would need a history table; explicitly deferred.
- **Bind-mount scanning by default** — stays opt-in (`DiskScanBinds`); the page surfaces "not
  measured" rather than forcing slow `du` walks.
- **Hard quotas** — already ruled out in the prior plan (no-ops on overlay2-over-ext4).

## Suggested commits

- `feat: aggregate volume total in app storage card and overview panel` (Parts 1–2)
- `feat: fleet-wide storage page with per-app totals and reclaim` (Part 3)

## Verification

- Parts 1–2: an app with volumes but no limit now shows a total; an app with a limit still shows the
  meter; a stateless app shows neither. An app with an unmeasured bind mount shows the floor + hint.
- Part 3: `/storage` lists apps sorted by usage matching the per-app cards; host breakdown matches
  Settings → Maintenance; reclaim frees the same bytes as the existing prune.
- `make lint typecheck test` clean; regenerate KB if `architecture.md`/`deployment-flow.md` touched
  (new endpoint → `/refresh-kb`).
