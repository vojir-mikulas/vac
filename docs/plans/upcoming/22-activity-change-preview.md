# 22 — Activity change preview (before/after diff window)

**Tier:** Trust / observability · **Effort:** M · **Status:** planned

## Goal

Add a **"Preview"** action to Activity feed entries that opens a window showing the **actual
change** — a **before → after** diff — for the entry. For an env-var edit you'd see which keys
were added/removed/changed; for an app-config change, which fields moved; for a base-domain
change, old vs. new. This makes the audit log answer "what *exactly* changed," not just "a
change happened," and gives the operator confidence before clicking **Revert**.

## Why it matters (strategy)

Trust is the moat. An audit feed that only says "updated environment for *app*" forces the
operator to trust without verifying. A real diff turns Activity from a log into an
accountability surface — and it pairs naturally with the existing revert (plan 11): preview
the change, then decide whether to undo it.

---

## Decisions (open questions, resolved)

1. **Server-computed diff endpoint** — `GET /api/audit/{id}/diff`. It centralizes
   secret-stripping and the "fetch current state" logic, and only runs when the operator
   clicks Preview. We do **not** expose raw `metadata.before` to the client.
2. **"After" is current DB state, labelled honestly as "current."** No frozen after-snapshot
   in the MVP (noted as a future extension below). The endpoint returns a `current_as_of`
   timestamp and a `changed_since` boolean hint so the UI can caveat it.
3. **One consistent diff component** keyed on a normalized field-row model. env, app-config,
   and base-domain all reduce to a list of `{label, status, before, after, masked}` rows, so a
   single `<DiffRows>` renders all three. env additionally carries add/remove/change status
   per key; app-config and base-domain are change-only rows.
4. **Reuse the `revert` snapshot structs** by exporting them from `api/internal/revert` (today
   they are unexported: `envSnap`/`envEntrySnap`, `appSnap`/`appFields`, `baseDomainSnap`).
   Both the reverter and the diff builder unmarshal the same types, so the before-shape can't
   drift. See "Backend, step 1."
5. **Preview is read-only and standalone for the MVP.** We do *not* fold it into the Revert
   confirmation flow yet (kept as a future extension) — but the diff component is built so a
   Revert dialog can embed it later with zero changes.

## The security constraint — never leak secrets

Env-var snapshots hold **sealed ciphertext** (base64), never plaintext, and the audit JSONB
isn't decrypted. The preview must keep it that way:

- Diff env vars by **key + sensitivity flag + changed/added/removed status**. For a key that is
  **non-sensitive in both before and current** (`!Sensitive && !WriteOnly`), the server *may*
  decrypt and show the value — this mirrors the existing rule in `ListAppEnv`
  (`handler/env.go:43`), which already decrypts non-sensitive values for display.
- For **sensitive or write-only** keys, render the value as `••••` with only "added" /
  "removed" / "value changed" status. Never decrypt these for the diff.
- The "changed" detection for sensitive keys compares **sealed bytes** (before base64 vs.
  current sealed) — ciphertext inequality is a safe proxy for "the value changed" without ever
  decrypting. (Note the harmless edge: re-sealing the same plaintext can produce different
  ciphertext under a nonce'd box, so a sensitive key may read as "changed" when the plaintext
  was identical. Acceptable — we never claim *what* changed, only that it did. Documented in
  the endpoint comment.)
- The endpoint **never** ships sealed/base64 ciphertext to the browser. The DTO carries only
  decrypted non-sensitive values or the `••••` mask.

---

## Current state — most of the data already exists

This is largely a **plumbing + UI** job, not new instrumentation.

- **Before-snapshots are already stored** under `audit_log.metadata.before` (JSONB) for the
  three curated/revertable actions:
  - **env replace** — full prior env set: key, sealed value (base64), `sensitive`,
    `write_only` (`handler/env.go:152`, via `envSnapshot`).
  - **app config** — name, git url/branch, compose file, build kind/config, mem limit
    (`handler/apps.go:340`, via `appConfigSnapshot`).
  - **base domain** — prior string (`handler/instance.go:164`).
- **The `revert` package already unmarshals these** (`api/internal/revert/revert.go`) — shapes
  are known and type-safe. Action dispatch keys off the audit `Action` suffix
  (`actionMatches`): `PUT …/apps/{id}/env`, `PATCH …/apps/{id}`, `PUT …/instance/base-domain`.
- **Metadata is deliberately stripped from the API DTO** (`handler/audit.go:18-32`,
  `auditLogDTO`) and from the TS type (`lib/api/audit.ts`). The diff endpoint is the safe,
  opt-in way to surface it.
- **Current-state queries already exist:** `store.ListEnvVarsForApp` (`store/env_vars.go:37`),
  `store.GetApp` (`store/apps.go:102`), `store.GetInstanceSettings`
  (`store/instance_settings.go:26`).
- **Decryption** is available via `*crypto.Box` (already passed to env handlers).
- **UI:** `ui/src/features/activity/activity-feed.tsx` renders one-line rows with a Revert
  button (`ActivityRow`); no detail view yet. Reusable `Dialog` in
  `ui/src/components/ui/dialog.tsx`; controlled pattern in `settings/domain-edit-dialog.tsx`.
- **API client:** `ui/src/lib/api/audit.ts` (query keys, `useActivity`, `useRevertActivity`).
- **Routes** registered in `api/internal/server/server.go:198-199`.
- **Mocks:** MSW handlers/db in `ui/src/mocks/` already model the audit feed + revert.

The **only "after" we ever fetch is current DB state** — DB is the source of truth.

---

## Implementation plan

### Backend

#### Step 1 — Export the snapshot structs from `revert` (no behaviour change)

In `api/internal/revert/revert.go`, rename the unexported snapshot types to exported ones so
the diff builder can reuse them, keeping before-shape in one place:

- `envEntrySnap` → `EnvEntry`, `envSnap` → `EnvSnapshot`
- `appFields` → `AppFields`, `appSnap` → `AppSnapshot`
- `baseDomainSnap` → `BaseDomainSnapshot`

Update internal references (`revertEnv`, `revertApp`, `revertBaseDomain`). Also export the
two small helpers the diff builder needs for action dispatch so the routing logic isn't
duplicated:

- `ActionMatches(action, method, suffix string) bool` (currently `actionMatches`)
- `TargetID(entry store.AuditLog) string` (currently `targetID`)

Pure rename + visibility change; the existing `revert` tests should pass untouched (adjust any
that reference the old names).

#### Step 2 — New `auditdiff` package: `api/internal/auditdiff/auditdiff.go`

A small, single-responsibility package that computes the normalized diff. It depends on the
store, `crypto.Box`, and the exported `revert` snapshot types.

```go
package auditdiff

// FieldStatus is the per-row change classification.
type FieldStatus string
const (
    StatusAdded     FieldStatus = "added"
    StatusRemoved   FieldStatus = "removed"
    StatusChanged   FieldStatus = "changed"
    StatusUnchanged FieldStatus = "unchanged" // env keys present + equal in both
)

// Row is one normalized before/after row, shared across all action kinds.
type Row struct {
    Label   string      `json:"label"`            // env key, app field name, or "base_domain"
    Status  FieldStatus `json:"status"`
    Before  *string     `json:"before,omitempty"` // nil when not applicable / masked
    After   *string     `json:"after,omitempty"`
    Masked  bool        `json:"masked"`           // true => value hidden (sensitive/write_only)
}

// Diff is the endpoint payload.
type Diff struct {
    Kind         string    `json:"kind"`          // "env" | "app" | "base_domain"
    Rows         []Row     `json:"rows"`
    CurrentAsOf  time.Time `json:"current_as_of"` // when "after" was read
    // ChangedSince is true if the audited entry was followed by a later change to
    // the same target, so "current" may not equal the immediate after of THIS action.
    ChangedSince bool      `json:"changed_since"`
}

type Store interface {
    GetAuditLog(ctx context.Context, id string) (store.AuditLog, error)
    ListEnvVarsForApp(ctx context.Context, appID string) ([]store.EnvVar, error)
    GetApp(ctx context.Context, id string) (store.App, error)
    GetInstanceSettings(ctx context.Context) (store.InstanceSettings, error)
}

type Builder struct { store Store; box *crypto.Box }
func New(s Store, box *crypto.Box) *Builder

// Compute resolves the entry, validates it carries a before-snapshot, fetches
// current state, and returns the normalized + sanitized diff. Returns
// ErrNotDiffable (→ 422) for non-curated/snapshotless entries, mirroring
// revert.ErrNotRevertable.
func (b *Builder) Compute(ctx context.Context, id string) (Diff, error)
```

`Compute` logic:

1. `GetAuditLog(id)` → `store.ErrNotFound` passes through (→ 404).
2. Unmarshal `metadata.before` (reuse the `metaShape{ Before json.RawMessage }` pattern from
   `revert.go:57`). Empty/absent → `ErrNotDiffable`.
3. Dispatch on action (reuse `revert.ActionMatches`):
   - `PUT …/apps/{id}/env` → `diffEnv(ctx, revert.TargetID(entry), before)`
   - `PATCH …/apps/{id}` → `diffApp(ctx, revert.TargetID(entry), before)`
   - `PUT …/instance/base-domain` → `diffBaseDomain(ctx, before)`
   - default → `ErrNotDiffable`.
4. `ChangedSince` MVP heuristic: cheap and honest. Set it `false` for now and label "current"
   in the UI (simplest correct option). *(Optional refinement: a store query "is there a later
   audit entry with the same target_id + action newer than this one?" — defer unless trivial.)*

`diffEnv` (the security-critical one):

- Unmarshal `revert.EnvSnapshot` from before. base64-decode each entry's sealed value.
- `ListEnvVarsForApp(appID)` for current (already sorted by key, sealed bytes).
- Build a key-union map. For each key:
  - present-before-only → `removed`; present-current-only → `added`; both → compare.
  - **value reveal rule:** if the key is non-sensitive & non-write-only in the relevant
    snapshot(s) **and** `box != nil`, `box.Open` the sealed bytes and set `Before`/`After`
    plaintext; `Masked=false`. Otherwise `Masked=true`, `Before`/`After` nil, value rendered
    as `••••` client-side.
  - **changed detection:** non-sensitive → compare decrypted plaintext; sensitive/write-only →
    compare sealed bytes (`bytes.Equal(beforeSealed, currentSealed)`). Equal → `unchanged`
    (still emit the row so the operator sees the full set; UI can collapse unchanged).
  - A key that is sensitive in one and not the other → treat as `changed`, `Masked=true`.
  - If `box == nil`, every value is masked (degrade gracefully, never 503 here — the diff is
    still useful as key+status).
- Sort rows: changed/added/removed first, then unchanged; alpha within group.

`diffApp`:

- Unmarshal `revert.AppSnapshot` (before). `GetApp(appID)` (current).
- Compare the seven fields (`name`, `git_url`, `git_branch`, `compose_file`, `build_kind`,
  `build_config`, `mem_limit_mb`). For each that differs, emit a `changed` Row with
  stringified before/after (`mem_limit_mb` nil → "unlimited"; `build_config` pretty-printed
  JSON or compact). Skip equal fields (app config has no add/remove concept). `Masked=false` —
  app config holds no secrets.
- Use a small helper to stringify each typed field consistently; keep field order stable and
  human (Name, Git URL, Branch, Compose file, Build kind, Build config, Memory limit).

`diffBaseDomain`:

- Unmarshal `revert.BaseDomainSnapshot` (before). `GetInstanceSettings()` (current).
- Single `changed` row, label "Base domain", before/after as strings ("" → "(cleared)").

#### Step 3 — Handler: `GET /api/audit/{id}/diff` in `handler/audit.go`

```go
// PreviewAudit returns a sanitized before→current diff for a curated audit entry
// (plan 22). Secrets never leave the server: sensitive/write-only env values are
// masked; only non-sensitive values are decrypted (same rule as ListAppEnv).
// 422 for a non-diffable entry, 404 if the entry is gone.
//
// GET /api/audit/{id}/diff
func PreviewAudit(db *auditdiff.Builder) http.HandlerFunc
```

- `chi.URLParam(r,"id")` → `db.Compute(...)`.
- Error mapping mirrors `RevertAudit`: `ErrNotFound`→404, `auditdiff.ErrNotDiffable`→422
  (`WriteErrorCode(..., "not_diffable", "no preview available for this action")`), else 500.
- `WriteJSON(w, 200, diff)`.

#### Step 4 — Wire the route + builder

In `server.go`, next to the existing audit routes (line 198-199):

```go
r.Get("/audit/{id}/diff", handler.PreviewAudit(auditdiff.New(s, box)))
```

`box` (`*crypto.Box`) is already in scope where env handlers are wired — confirm and thread it
through; if not in scope at that block, pass it the same way `ReplaceAppEnv` receives it.

#### Step 5 — Backend tests

- `api/internal/auditdiff/auditdiff_test.go` (unit, fake Store):
  - env: added / removed / changed / unchanged classification; **sensitive value never
    decrypted** (assert `Masked` + nil Before/After + `••••` not present as plaintext);
    non-sensitive value decrypted and shown; `box == nil` masks everything.
  - app: only changed fields emitted; `mem_limit_mb` nil→"unlimited"; build_config diff.
  - base_domain: cleared vs set.
  - non-curated action / missing before → `ErrNotDiffable`.
  - sealed-bytes-changed-but-same-plaintext sensitive key reads as `changed` (documented).
- Handler test for the 404 / 422 / 200 mapping (table-driven, alongside existing audit handler
  tests if present).

### Frontend

#### Step 6 — API client (`ui/src/lib/api/audit.ts`)

Add the diff type + fetch + a lazy query hook:

```ts
export type DiffStatus = 'added' | 'removed' | 'changed' | 'unchanged'
export interface DiffRow {
  label: string
  status: DiffStatus
  before?: string
  after?: string
  masked: boolean
}
export interface ActivityDiff {
  kind: 'env' | 'app' | 'base_domain'
  rows: DiffRow[]
  current_as_of: string
  changed_since: boolean
}

auditApi.diff = (id: string) => api.get<ActivityDiff>(`audit/${id}/diff`)

export function useActivityDiff(id: string | null) {
  return useQuery({
    queryKey: [...queryKeys.activity, 'diff', id],
    queryFn: () => auditApi.diff(id!),
    enabled: !!id,            // lazy: only fetch when a preview is open
    staleTime: 0,             // always current
  })
}
```

`queryKeys.activity` already exists; the diff key extends it.

#### Step 7 — Which entries get a Preview button

Only entries that carry a before-snapshot — i.e. the curated/revertable set. The DTO does
**not** currently distinguish "was revertable" from "still revertable" (`revertable` is
`Revertable && RevertedAt == nil`, `handler/audit.go:99`), so a *reverted* entry loses its
revert button but should still be previewable.

**Add one boolean to the DTO + TS type:** `has_preview` (or `diffable`) =
`a.Revertable` (the raw column, independent of `RevertedAt`). Set it in `toAuditDTO`. This is
the clean signal for showing Preview, and it lets reverted entries still be inspected.
(`Revertable` the column marks exactly the snapshotted set.)

#### Step 8 — Diff dialog component

New `ui/src/features/activity/activity-diff-dialog.tsx`, controlled like
`domain-edit-dialog.tsx`:

```tsx
export function ActivityDiffDialog({ entry, onClose }: { entry: AuditEntry; onClose: () => void })
```

- `const { data, isLoading, error } = useActivityDiff(entry.id)` (fetched lazily on open).
- `<Dialog open onOpenChange={(o)=>!o && onClose()}>` → `<DialogContent className="sm:max-w-2xl">`.
- Header: title `Change preview`, description = humanized action / summary.
- Body states: skeleton while loading; error message on failure; else `<DiffRows diff={data} />`.
- Footer: a muted caveat line — "Compared against current state" + `current_as_of` relative
  time; plus a Close button. (If we later add `changed_since`, show "⚠ changed again since
  this action" when true.)

`<DiffRows>` — the single consistent renderer for all three kinds:

- A two-column **before → after** layout (label column + before / after cells), `font-mono
  text-xs`, one row per `DiffRow`.
- Status pill per row: added (green) / removed (red) / changed (amber) / unchanged (muted).
- `masked` rows render both sides as `••••`; for `added`/`removed` masked, show `••••` on the
  populated side and `—` on the empty side.
- For `base_domain` and single-field changes the same row layout reads fine (one or few rows),
  satisfying "one consistent component."
- Optionally collapse `unchanged` env rows behind a "Show N unchanged" toggle to keep large
  env sets readable.

#### Step 9 — Hook the button into `ActivityRow`

In `activity-feed.tsx`:

- Add local state in `ActivityFeed`: `const [preview, setPreview] = useState<AuditEntry|null>(null)`.
- In `ActivityRow`, render a **Preview** button (icon `Eye` from lucide) when
  `entry.has_preview`, placed left of the Revert button. It's shown for both revertable and
  already-reverted entries; Revert stays gated on `entry.revertable`.
- Render `{preview && <ActivityDiffDialog entry={preview} onClose={() => setPreview(null)} />}`
  once at the feed level.

#### Step 10 — Mocks (`ui/src/mocks/`)

- Add an MSW handler for `GET /api/audit/:id/diff` returning a representative `ActivityDiff`
  per `kind` (env with mixed statuses incl. a masked sensitive key, app multi-field, base
  domain). Drive off the mock db entry's action so the kind matches.
- Add `has_preview` to the mock audit entries (true for the curated actions).
- Update `ui/src/mocks/types.ts` / `db.ts` shapes; extend `mocks.test.ts` if it asserts the
  audit shape.

#### Step 11 — Frontend checks

- `make typecheck`, `make lint`.
- A vitest component test for `<DiffRows>` asserting masked rows show `••••` and never the
  underlying value, and that status pills render per status.

---

## Acceptance

In the Activity feed, an env / app-config / base-domain entry shows a **Preview** button
(present even after it's been reverted). Clicking it opens a diff window:

- **env** — added / removed / changed / unchanged keys; sensitive & write-only values masked
  as `••••` (status only), non-sensitive values shown.
- **app config** — only the changed fields, old → new (mem limit nil → "unlimited").
- **base domain** — old → new (empty → "(cleared)").

Non-curated actions (deploys, backups, addons, triggers) have **no** Preview button. **No
sealed secret or ciphertext ever reaches the browser** — verified by the `auditdiff` unit
tests and the `<DiffRows>` masking test. The "after" side is labelled as current state.

## Files touched (summary)

**Backend**
- `api/internal/revert/revert.go` — export snapshot structs + `ActionMatches`/`TargetID`.
- `api/internal/auditdiff/auditdiff.go` (new) + `auditdiff_test.go` (new).
- `api/internal/server/handler/audit.go` — `PreviewAudit` handler + `has_preview` on DTO.
- `api/internal/server/server.go` — register `GET /audit/{id}/diff`.

**Frontend**
- `ui/src/lib/api/audit.ts` — diff type, `auditApi.diff`, `useActivityDiff`, `has_preview`.
- `ui/src/features/activity/activity-diff-dialog.tsx` (new) — dialog + `<DiffRows>`.
- `ui/src/features/activity/activity-feed.tsx` — Preview button + dialog wiring.
- `ui/src/mocks/{db,types}.ts`, `ui/src/mocks/handlers` — diff endpoint + `has_preview`.

## Future extensions (explicitly out of scope for MVP)

- **Frozen "after" snapshot** at write time for a point-in-time diff (removes the
  time-skew caveat); would also let `changed_since` be exact.
- **Preview inside the Revert flow** — embed `<DiffRows>` in a Revert confirmation dialog
  ("here's what reverting will restore"). The component is built to drop in unchanged.
- **Diffs for more action kinds** if/when they gain before-snapshots.
