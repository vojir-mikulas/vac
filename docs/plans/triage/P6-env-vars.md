# P6 — Env vars (detailed plan)

**Track:** P6 (see [`00-parallel-tracks.md`](00-parallel-tracks.md)) · **Status:** ready to build
**Owns:** the env write/read path only — `server/handler/env.go`, `store/env_vars.go`, the env-var
snapshot in `internal/revert/revert.go`, migration `00061`, and the env editor UI
(`ui/src/features/app-detail/env-tab.tsx`, `ui/src/lib/api/env.ts`, `ui/src/types/api.ts`).
**Source plan:** [env-vars.md](env-vars.md).

Single item, **P6.1**. Most isolated track in the triage set — it touches no shared DTO, no
`services.go`, no addon template. The one cross-track constraint is the migration number.

> **Migration:** claim **`00061`** (latest on disk is `00060_managed_db_binding_unique.sql`, taken
> by P1.1). Adds `write_only BOOLEAN NOT NULL DEFAULT false` to `env_vars`. Default `false` means
> **every existing row and every unchanged code path keeps today's behavior** — masked + reveal,
> audit-logged. The flag is purely opt-in.

---

## Reality check (read before coding — the current behavior is already reasonable)

The triage note asked: *"should encrypted env vars be recoverable? Right now I can just preview
them again and copy them out."* Confirmed against source — **this is policy, not a bug:**

- Every value is sealed at rest with `crypto.Box` regardless of the flag (`env.go:151`,
  `box.Seal`). The `sensitive` flag governs **display only**.
- `ListAppEnv` (`env.go:31`) omits `Value` for `sensitive=true` rows (`env.go:41-53`); it's only
  returned for non-sensitive keys.
- Revealing a sensitive value is a separate, explicit call — `RevealAppEnv` (`env.go:63`) — and it
  is audit-logged: `slog.Info("env var revealed", …)` (`env.go:86`, value itself kept out of the
  log).

So sensitive values are already masked and reveal is a deliberate, logged action. The current
default is correct and **must not change**. P6.1 adds an **optional, opt-in "write-only / no-reveal"
mode** on top, for operators who want a true one-way secret.

### The one non-obvious design tension (this drives the whole plan)

The env editor saves via a **full-replace PUT** (`ReplaceAppEnv`, `env.go:105`): the body carries
*every* var with its plaintext value, and the store wipes + reinserts (`ReplaceEnvVars`,
`env_vars.go:55`). To make that work for sensitive vars the operator never revealed, `save()` in the
UI **fetches the plaintext of every unresolved row via `reveal`** before building the payload
(`env-tab.tsx:194-206`).

A write-only secret can **never** be revealed — so on the next save the UI has no plaintext to
re-send, and the full-replace would blank it. **A write-only var therefore needs a "keep the
existing sealed value" path through the PUT** — the value is preserved without ever being decrypted.
There's direct precedent: the revert reverter already feeds *sealed* bytes straight back into
`ReplaceEnvVars` without decrypting (`revert.go:134-142`). P6.1 generalizes that one mechanism to
the live PUT.

---

## P6.1 — Optional write-only / no-reveal secret toggle · Effort S–M

### Semantics (define these up front)

A var marked **write-only** is:
- **Sealed at rest** — same as every row today (no change).
- **Never returned** — omitted from `ListAppEnv`, and `RevealAppEnv` **refuses** it (403).
- **Set/replaceable** — you can write a new value (re-seals) or delete it; you just can't read it
  back. Matches the brief's "set/replace but never read."
- **A one-way latch** — once persisted write-only, it **cannot be downgraded** to
  sensitive/plaintext (that would let someone clear the flag and then reveal, defeating the
  promise). To change the mode, delete and recreate. Enforced server-side.
- **Implies `sensitive`** — write-only is the stronger form. Normalize `write_only=true ⇒
  sensitive=true` at the handler so list-omission logic stays a single rule.

Default unchanged: a var with `write_only=false` (every existing row) behaves exactly as today.

### Migration — `00061_env_vars_write_only.sql`

```sql
-- +goose Up
ALTER TABLE env_vars ADD COLUMN write_only BOOLEAN NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE env_vars DROP COLUMN write_only;
```

Mirrors `00016_env_vars_sensitive.sql`. Default `false` = no behavior change for existing rows.

### Store — `store/env_vars.go`

- `EnvVar` struct (`:15`): add `WriteOnly bool`.
- `EnvVarInput` struct (`:26`): add `WriteOnly bool`.
- `ListEnvVarsForApp` (`:32`) and `GetEnvVar` (`:96`): add `write_only` to the `SELECT` column list
  and the `rows.Scan` / `QueryRow().Scan` targets.
- `ReplaceEnvVars` (`:55`): add `write_only` to the `INSERT` column list and `$5`.
- `UpsertEnvVar` (`:78`) — **leave unchanged.** It's used only by managed-DB provisioning to inject
  `DATABASE_URL` (never write-only); the new column's `DEFAULT false` covers the insert path and the
  `ON CONFLICT … DO UPDATE` leaves `write_only` untouched. No signature churn.

### Handler — `server/handler/env.go`

- **`envVarDTO`** (`:20`): add `WriteOnly bool json:"write_only"`.
- **`ListAppEnv`** (`:31`): set `dto.WriteOnly = v.WriteOnly`; widen the value-omission guard from
  `if !v.Sensitive` to `if !v.Sensitive && !v.WriteOnly` (write-only never ships a value).
- **`RevealAppEnv`** (`:63`): after loading the row, if `v.WriteOnly` →
  `WriteError(w, http.StatusForbidden, "this secret is write-only and cannot be revealed")`
  **before** decrypting. (Keep the existing `slog.Info("env var revealed", …)` only on the success
  path — a refused reveal isn't a disclosure.)
- **`putEnvEntry`** (`:91`): add two fields:
  - `WriteOnly bool json:"write_only"`
  - `Keep bool json:"keep"` — "reuse the existing sealed value for this key; don't re-seal `Value`."
- **`ReplaceAppEnv`** (`:105`): it already loads the prior set for the revert snapshot
  (`prior, err := s.ListEnvVarsForApp` at `:129`). Reuse that — build
  `priorByKey := map[string]store.EnvVar` once. Then in the per-entry loop (`:136-157`):
  1. Normalize: `if e.WriteOnly { e.Sensitive = true }`.
  2. **Downgrade guard:** if `priorByKey[k].WriteOnly && !e.WriteOnly` →
     `400 "cannot downgrade a write-only secret; delete and recreate it"`.
  3. **Keep path:** if `e.Keep` → require `k` in `priorByKey` (else `400 "keep set for unknown key"`)
     and reuse `priorByKey[k].Value` (the sealed bytes) instead of calling `box.Seal`. This is the
     mechanism that lets a never-revealable secret survive a full-replace.
  4. Otherwise seal `e.Value` as today.
  5. Append `store.EnvVarInput{…, WriteOnly: e.WriteOnly}`.

  No new audit plumbing — `ReplaceAppEnv` is already audited ("replaced environment for {slug}",
  `:133`) and revertable. Just make sure the snapshot carries the flag (below) so the audit diff and
  revert both reflect write-only state.
- **`envVarSnap`** (`:170`) + **`envSnapshot`** (`:176`): add `WriteOnly bool json:"write_only"` and
  populate it. Keep `Value` as base64 of the **sealed** bytes (unchanged) — plaintext must never
  reach `audit_log`.

### Revert — `internal/revert/revert.go`

`revertEnv` (`:126`) feeds the snapshot back through `ReplaceEnvVars`. So write-only state round-trips:
- `envEntrySnap` (`:116`): add `WriteOnly bool json:"write_only"`.
- In the loop (`:134-141`): set `WriteOnly: e.WriteOnly` on the `store.EnvVarInput`. Sealed bytes
  already round-trip — no decryption, so a write-only secret reverts correctly without ever being read.

### Frontend

**`ui/src/types/api.ts`** `EnvVar` (`:155`): add `write_only?: boolean`.

**`ui/src/lib/api/env.ts`** `EnvVarInput` (`:9`): add `write_only: boolean` and `keep?: boolean`.

**`ui/src/features/app-detail/env-tab.tsx`** — the real work is the save path and the row affordances:

- **`Row`** (`:32`): add `writeOnly: boolean`. Seed from `v.write_only` (`:66-74`); a write-only row
  loads with `value: null` (never held) and `revealed: false`.
- **`reveal()`** (`:88`) and **`copyValue()`** (`:139`): no-op / hidden for write-only rows — both
  would 403. (Drive this off the row flag in the UI so the buttons don't render; see `EnvRow`.)
- **`toggleSensitive()`** (`:103`): a *persisted* write-only row's lock is a no-op (one-way latch).
- **`save()`** (`:176`) — the critical change. Today it resolves plaintext for every `value===null`
  row via `reveal` (`:194-206`); that 403s for write-only. Instead, per row build the payload entry:
  - **Persisted write-only, value untouched** (`!isNew && writeOnly && value===null`) → send
    `{ key, write_only: true, sensitive: true, keep: true }` (no value; backend reuses sealed bytes).
  - **New write-only, or a write-only row the operator typed a new value into** (`value !== null`) →
    send the plaintext with `write_only: true, keep: false` (re-seals).
  - **Everything else** → unchanged (resolve via reveal as today, `write_only: false`, `keep: false`).
  - So the reveal-resolution loop must **skip rows where `writeOnly` is true** — only non-write-only
    sensitive rows still get fetched.
- **`EnvRow`** (`:332`): three visual states instead of two.
  - *plaintext* / *sensitive* — unchanged (lock toggle, eye reveal, copy).
  - *write-only* — value input shows a fixed `••••••••` and is **editable only to replace**
    (typing sets a new plaintext + `keep:false`); **no reveal (eye), no copy**; render a
    non-interactive **ShieldLock** badge labelled "Write-only — cannot be revealed". Only **Delete**
    (and replace-value) remain.
  - **Upgrade affordance:** on a *sensitive* (non-write-only) row, offer a distinct "Make write-only"
    action (e.g. a `ShieldLock` toggle, lucide already imports `Lock`/`LockOpen` — add `ShieldAlert`
    or similar). Confirm before applying (it's effectively irreversible — clarify "you won't be able
    to reveal or copy this value afterward"). New rows can be created write-only the same way.
- **"About environment" sidebar** (`:314-325`): add one sentence — *"Mark a secret **write-only** to
  make it unrevealable: you can replace or delete it, but VAC will never show or copy its value
  again."*

### Tests

- **store** (`env_vars_test.go`): `ReplaceEnvVars` round-trips `write_only`; `ListEnvVarsForApp` /
  `GetEnvVar` return it.
- **handler** (`env_test.go`):
  - `ReplaceAppEnv` with `write_only:true` seals the value; `ListAppEnv` omits `value` and reports
    `write_only:true`.
  - `RevealAppEnv` on a write-only key → **403** (and no "env var revealed" log).
  - `keep:true` reuses the prior sealed bytes (value unchanged across a replace that omits it);
    `keep:true` on an unknown key → 400.
  - Downgrade attempt (prior `write_only:true`, request `write_only:false`) → **400**.
  - Sanity: a plain replace of non-write-only vars still works (regression guard on the new loop).
- **revert** (`revert_test.go`): extend the existing env case (`:76-99`) so a snapshot with a
  write-only row reverts with `WriteOnly:true` preserved and sealed bytes round-tripped.
- **UI** (RTL or manual): a write-only row renders the shield badge, no eye/copy buttons, masked
  value; saving an untouched write-only row sends `keep:true` and **no** reveal call fires.

### Acceptance

- **Default unchanged:** sensitive vars stay masked, reveal still works and is still audit-logged.
- A var can be marked **write-only**; afterward `ListAppEnv`/`RevealAppEnv` never return its value
  (reveal → 403), and the UI offers no reveal/copy.
- A write-only var can still be **replaced** (new value re-seals) or **deleted**, and an untouched
  write-only var **survives a full-replace save** (via `keep`) without being decrypted.
- A write-only var **cannot be downgraded** (server rejects it; delete + recreate to change mode).
- Write-only state **survives an audit revert** with no plaintext ever landing in `audit_log`.

---

## Cross-track sync points P6 touches

- **Migrations** ([#5](00-parallel-tracks.md)) — P6 claims **`00061`** (P1.1 holds `00060`). One
  numbered sequence; no other triage track adds a migration.
- **No other collisions.** P6 touches none of the shared seams: not the App DTO
  ([#1](00-parallel-tracks.md)), not `services.go` ([#2](00-parallel-tracks.md)), not the App
  Settings UI ([#3](00-parallel-tracks.md)) — the env editor is its own tab — and not the addon
  template ([#4](00-parallel-tracks.md)). Safest track to hand to a parallel agent.

## Suggested PR breakdown

Single PR is fine given the size — but if split, draw the line at the API boundary:

1. `feat(env): add opt-in write-only secrets (no-reveal)` — migration `00061` + store +
   `env.go` (DTO, list omission, reveal 403, `keep`/downgrade logic) + revert snapshot field +
   backend tests.
2. `feat(env): surface write-only toggle in the env editor` — UI (`EnvVar`/`EnvVarInput` types,
   `Row` flag, `save()` `keep` path, `EnvRow` shield state + upgrade affordance, sidebar copy).

PR 1 is self-contained and testable headless; PR 2 is pure UI over it. This is **net-new polish**,
not one of the four critical-path bugs — it can land any time after Stage 0 (and doesn't even depend
on Stage 0, since it touches neither the app DTO nor shared Settings UI).
