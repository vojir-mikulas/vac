# 14 — CI / GitHub Actions workflow cleanup

**Tier:** Dev-experience · **Effort:** S · **Status:** ✅ shipped (Track F1 — bench-ram off PRs, `.github/actions/setup`, consolidated `release.yml`)

## Goal

Reduce the work that fires on every push/PR, and consolidate the release path so
"what happens on a tag" lives in one file. Same correctness guarantees, less churn
and fewer wasted runner minutes.

## Current state (commit `8defbb4`)

Three workflows in `.github/workflows/`:

| File | Triggers | Jobs |
|------|----------|------|
| `ci.yml` | push→`main`, **every PR**, `workflow_dispatch` | `lint`, `test`, `bench-ram` |
| `release.yml` | tag `v*`, `workflow_dispatch` | `build` (matrix: `vac-api`, `vac-proxy` → GHCR, multi-arch) |
| `publish-installer.yml` | tag `v*` | `publish` (install scripts + compose → gh-pages) |

### Pain points

1. **`bench-ram` runs on every push and PR.** It boots a Docker stack and waits
   `SETTLE_SECONDS: 90`, so it's by far the slowest, heaviest job — gating routine
   PRs that don't touch RAM behavior. (Harness defined in plan
   [`07-ram-benchmark-harness.md`](../done/07-ram-benchmark-harness.md).)
2. **`lint` and `test` repeat identical setup** (checkout → Go → pnpm → node →
   `pnpm install --frozen-lockfile`). Setup cost paid 3× across the file.
3. **Two workflows fire on the same `v*` tag.** Release logic is split for no real
   reason; concurrency control exists only on `ci.yml`.

## Plan

### 1. Move `bench-ram` off the PR hot path

- Trigger `bench-ram` on push→`main` + `workflow_dispatch` only (drop it from PRs),
  plus an optional nightly `schedule` (e.g. `cron: "0 3 * * *"`).
- Net: PRs run only `lint` + `test` (fast); the RAM guard still protects `main` and
  runs nightly so regressions surface within a day.

**Option A (simplest):** keep `bench-ram` as a job in `ci.yml` but guard its steps
with `if: github.event_name != 'pull_request'`. **Option B (cleaner):** split it into
`bench-ram.yml` with its own `on: { push: { branches: [main] }, schedule:, workflow_dispatch: }`.
→ **Recommend B** — keeps `ci.yml` focused on PR gating and makes the bench's
independent cadence explicit.

### 2. Skip docs-only churn

- Add `paths-ignore: ["docs/**", "**/*.md"]` to `ci.yml`'s push + PR triggers so
  README/KB edits don't spin up the full matrix.
- Caveat: `paths-ignore` only applies to `push`/`pull_request` — fine here. If a
  required status check is configured on the repo, a docs-only PR will show the check
  as "skipped"; confirm branch protection treats skipped as passing (it does by
  default for path-filtered runs).

### 3. De-duplicate CI setup

- Add a local composite action `.github/actions/setup/action.yml` that does:
  checkout assumed by caller → `setup-go` (1.25, cache `api/go.sum`) →
  `pnpm/action-setup` (10.14.0) → `setup-node` (22, cache pnpm) →
  `pnpm install --frozen-lockfile`.
- `lint` and `test` jobs each `- uses: actions/checkout@v4` then
  `- uses: ./.github/actions/setup`. Keeps the two jobs **parallel** (speed) while
  removing the duplication.

### 4. Consolidate the two tag workflows

- Merge `publish-installer.yml` into `release.yml` as a second job
  (`installer-assets`), under the shared `on: { push: { tags: ["v*"] }, workflow_dispatch: }`.
- Delete `publish-installer.yml`.
- Keep job-level `permissions` minimal and per-job: `images` needs
  `packages: write`; `installer-assets` needs `contents: write` (gh-pages push). Set
  them at job scope rather than widening the whole file.
- Add a `concurrency` group (e.g. `group: release-${{ github.ref }}`,
  `cancel-in-progress: false`) so a re-pushed tag doesn't double-build, but an
  in-flight release isn't aborted mid-publish.

## Out of scope (considered, rejected)

- **Path-filtering Go vs UI jobs.** The UI is `go:embed`-bundled into the binary and
  `lint`/`test` already span both languages; splitting by path adds matrix complexity
  for little gain on a repo this size.
- **Merging `lint` + `test` into one job.** Loses parallelism; the composite action
  (step 3) gets the DRY benefit without the latency cost.

## Acceptance

- Opening a code PR triggers only `lint` + `test`; `bench-ram` does **not** run.
- Pushing to `main` runs `lint`, `test`, and `bench-ram`.
- A docs-only PR triggers no CI matrix run (or a cleanly-skipped one).
- Pushing a `v*` tag runs a single `release.yml` that both publishes GHCR images and
  the installer assets; `publish-installer.yml` is gone.
- `lint` and `test` share one composite setup; no duplicated setup steps remain.

## Touch list

- `.github/workflows/ci.yml` (trigger/paths edit; remove or guard `bench-ram`)
- `.github/workflows/bench-ram.yml` (new, if Option B)
- `.github/actions/setup/action.yml` (new composite action)
- `.github/workflows/release.yml` (add `installer-assets` job, concurrency)
- `.github/workflows/publish-installer.yml` (delete)
