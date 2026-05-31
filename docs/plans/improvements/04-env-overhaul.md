# 04 — Environment variables overhaul (Vercel-style)

**Goal:** A proper key/value env editor where each variable is either **plaintext
(readable)** or **sensitive (encrypted, masked)** — like Vercel. Keep bulk `.env`
import, and add an **opt-in** auto-detect that marks token/secret-like keys as
sensitive on import.

## Current state

- **Storage:** separate `env_vars` table (migration `00010_env_vars.sql`). **All**
  values are sealed with `crypto.Box` (AES-256-GCM, `VAC_MASTER_KEY`) in
  `api/internal/store/env_vars.go`. The store never holds plaintext.
- **API:** `PUT /api/apps/{id}/env` replaces the full set
  (`handler/env.go` `ReplaceAppEnv`); `ListAppEnv` returns **keys only, no
  values** — so the UI can never display a value. PUT semantics (full replace).
- **UI:** values render masked (`●●●●`); there is no per-key sensitivity, no
  reveal, no inline editor matching the design
  (`design/project/src/view-app-detail-tabs.jsx` `TabEnvironment`, which shows
  per-row `secret` flag, eye/eye-off reveal, copy, and a `.env` drop zone).

## Backend changes

- **Schema:** add `sensitive BOOLEAN NOT NULL DEFAULT true` to `env_vars` (new
  goose migration). Default `true` is the safe choice (existing rows stay
  masked/encrypted).
- **Value storage by sensitivity:**
  - **Sensitive:** keep current `crypto.Box` sealing; value is never returned by
    the list endpoint (reveal requires an explicit decrypt call — see below).
  - **Plaintext:** store readable. Two viable approaches — pick one and record in
    `docs/deviations.md`:
    1. **Simplest:** store plaintext values in a `value_plain TEXT` column;
       `value_sealed` stays for sensitive. Clear separation, easy to return.
    2. **Uniform:** keep sealing everything at rest (defense in depth even for
       "plaintext") but allow the API to return decrypted values for
       non-sensitive keys. More consistent with the "encrypted at rest"
       invariant in CLAUDE.md — **recommended**, since it preserves the
       at-rest-encryption posture while still letting the UI display non-sensitive
       values.
  - The injected `.env` rendered at deploy (`RenderEnvFile`, `deploy/`) is
    unaffected — it already decrypts everything to write the file.
- **API:**
  - `ListAppEnv` returns, per key: `{key, sensitive}` and, for **non-sensitive**
    keys, the `value`. Sensitive keys still return no value.
  - Add a reveal endpoint for sensitive values:
    `GET /api/apps/{id}/env/{key}/reveal` → returns the decrypted value for a
    single key (audit-log the reveal; gate behind auth as usual). This backs the
    eye-toggle on sensitive rows.
  - `PUT /api/apps/{id}/env` payload gains `sensitive` per entry; keep full
    replace semantics. (Optional later: PATCH single key — not required now.)
- **Auto-detect (server-side validation):** the auto-detect runs client-side on
  import (below), but the server should also accept the per-entry `sensitive`
  flag verbatim — no server-side forcing. Keep server as source of truth for
  what was stored.

## UI changes

Rebuild the Environment tab to the design:
- **Editor grid:** rows of `KEY | value | actions`. Sensitive rows render masked
  with an eye/eye-off toggle (calls the reveal endpoint on demand), non-sensitive
  rows show the value inline and are directly editable. Per-row: copy, delete,
  toggle sensitive. "Add variable" row.
- **Sensitive toggle per key:** a lock/switch on each row flips `sensitive`.
- **Bulk `.env` import:** keep the existing paste/drop-whole-file flow. Parse
  `KEY=VALUE` lines into rows.
  - **Opt-in auto-detect:** a checkbox in the import UI — "Auto-mark secrets"
    (default on or off — operator's call; suggest **on**). When enabled, mark a
    key `sensitive` if its name matches a heuristic: case-insensitive contains
    `SECRET|TOKEN|KEY|PASSWORD|PASS|PRIVATE|CREDENTIAL|API_KEY|AUTH|DSN|CONNECTION`
    (tune the list). User can flip any row afterward before saving.
  - Show a preview of parsed rows with their detected sensitivity before commit.
- **Unsaved/restart hint:** the design shows "N unsaved changes pending restart".
  Mirror that — env changes apply on next deploy/restart; surface that note
  (`ui/src/features/app-detail/` env tab).
- Move heuristic + parsing into a small testable util (`ui/src/features/.../env/`)
  with vitest coverage.

## Acceptance criteria
- A non-sensitive var's value is visible/editable inline; a sensitive var is
  masked and revealable via the eye toggle (server round-trip).
- Bulk-importing a `.env` with auto-detect on marks `DATABASE_PASSWORD`,
  `API_TOKEN`, etc. as sensitive and leaves `NODE_ENV`, `PORT` plaintext.
- Auto-detect can be turned off; then everything imports with a chosen default.
- At-rest encryption posture documented (chosen approach) in `docs/deviations.md`.

## Verification
Go tests for the new store column + reveal endpoint; vitest for the parser +
heuristic; manual: import a `.env`, toggle sensitivity, reveal, redeploy and
confirm injected values.
