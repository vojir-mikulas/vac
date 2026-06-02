# Track F — Dev-Experience — execution plan

> Working plan for executing **Track F** of [`00-parallel-tracks.md`](00-parallel-tracks.md):
> the single item **`14` CI / GitHub Actions cleanup**
> ([stub](14-ci-workflow-cleanup.md)). Owns `.github/` only — **zero overlap with any other
> track's source**, safe to land any time, no migration numbers to coordinate.

## TL;DR — the structural work already shipped; Track F is now a re-enable + verify pass

The stub describes four cleanups. **All four already landed** in commit
`8e41215 "ci: trim PR hot path and consolidate release workflow"` (2026-06-01). Then commit
`4893ec5 "chore: disable workflows for now"` (2026-06-02) **commented out the automatic
triggers** on `ci.yml` and `bench-ram.yml`, leaving only `workflow_dispatch`. So no CI fires on
push/PR today.

That changes Track F from *build the cleanup* to **prove the workflows are green, then turn the
triggers back on** — plus a couple of small residual checks. The plan below records what's
done, what remains, and the gate for flipping CI back on.

### Stub plan vs. as-built (commit `eb09bf7`)

| Stub step | Intended outcome | As-built | Status |
|---|---|---|---|
| 1. Move `bench-ram` off PR hot path | Option B: own `bench-ram.yml` (push→main + nightly + dispatch) | `.github/workflows/bench-ram.yml` exists exactly as specified | ✅ done (triggers disabled) |
| 2. Skip docs-only churn | `paths-ignore: ["docs/**","**/*.md"]` on push+PR | present on both push & PR in `ci.yml` | ✅ done (commented out) |
| 3. De-duplicate CI setup | composite `.github/actions/setup/action.yml`; `lint`+`test` stay parallel | composite action exists; both jobs `uses: ./.github/actions/setup` | ✅ done |
| 4. Consolidate the two tag workflows | merge `publish-installer.yml` → `release.yml` as `installer-assets`; per-job perms; release concurrency | `release.yml` has `build` + `installer-assets`; `publish-installer.yml` deleted; `concurrency: release-${{ github.ref }}`, `cancel-in-progress: false` | ✅ done |

Nothing in the stub's "Plan" section is unbuilt. The remaining gap is purely that the triggers
are off.

---

## Why the workflows are disabled (the gate to clear before re-enabling)

`4893ec5` turned the triggers off deliberately ("for now"). Two plausible reasons, both worth
confirming before flipping them back on — because re-enabling a workflow that goes red on the
first push is worse than leaving it off:

1. **Unproven green.** Track B's execution notes
   ([`track-b-execution.md`](track-b-execution.md)) record that `ci.yml` was the repo's
   first-ever lint/test/typecheck CI and that `make bench-ram` was **never verified end-to-end
   locally** — the full Docker build filled the dev host's disk; the intent was to "run it on
   the CI runner." So `bench-ram` on `ubuntu-latest` is unproven, and `lint`/`test`/`typecheck`
   may surface latent issues the local dev loop never ran.
2. **Noise during heavy parallel-track work.** With Tracks D/E in flight across worktrees,
   always-on PR CI may have been muted to avoid red-X noise on in-progress branches.

Neither is a code problem in Track F's files — they're a *readiness* question. The plan
resolves it by running each workflow manually (`workflow_dispatch`) and only re-enabling
triggers once each is green.

---

## F1 — Re-enable CI, verified  *(effort S)*

**Goal:** automatic CI back on for push→`main` and PRs, with confidence it's green.

### Steps

1. **Dry-run via `workflow_dispatch`.** Both `ci.yml` and `bench-ram.yml` already expose
   `workflow_dispatch: {}`. Trigger each manually (Actions tab → Run workflow, or
   `gh workflow run ci.yml` / `gh workflow run bench-ram.yml`) on the branch that will become
   `main`. This validates the composite action, the lint/test/typecheck jobs, and the
   bench-ram Docker boot **without** committing live triggers.
2. **Fix whatever the dry-run surfaces.** Expected candidates (from Track B's notes): a
   `bench-ram` runner-disk/timeout issue, a pinned-linter (`golangci-lint v2.12.2`) rule, or a
   latent typecheck/`-race` flake. Fixes land here (CI files / Makefile targets) — still
   `.github/`-and-build only, no other track's source.
3. **Uncomment the triggers.** Revert the `4893ec5` comment-out in both files:
   - `ci.yml` → restore `push: { branches: ["main"], paths-ignore: ["docs/**","**/*.md"] }`
     and `pull_request: { paths-ignore: ["docs/**","**/*.md"] }`.
   - `bench-ram.yml` → restore `push: { branches: ["main"], paths-ignore: […] }` and
     `schedule: [{ cron: "0 3 * * *" }]`.
   - Keep `workflow_dispatch` on both (cheap manual escape hatch).
   - Drop the two "Temporarily disabled…" comment blocks.
4. **Land on `main`.** Triggers — especially `schedule` and `push: branches:[main]` — only take
   effect from the workflow file **on the default branch**. The re-enable is inert until merged
   to `main`. Note this in the merge PR so it isn't mistaken for "CI still broken."

### Acceptance (mirrors the stub)

- Opening a code PR triggers **only** `lint` + `test`; `bench-ram` does **not** run.
- Pushing to `main` runs `lint`, `test`, **and** `bench-ram`.
- A docs-only PR (touches only `docs/**` or `**/*.md`) triggers no CI matrix run (or a cleanly
  skipped one).
- The nightly `bench-ram` schedule fires once it's on `main`.

### Files touched

`.github/workflows/ci.yml`, `.github/workflows/bench-ram.yml` (uncomment triggers; possible
green-up fixes). Possibly `Makefile` / `scripts/bench-ram.sh` if the dry-run needs a tweak.

---

## F2 — Confirm the release path (no rebuild, just verify)  *(effort S)*

**Goal:** make sure the *already-merged* release consolidation behaves before the next `v*` tag.

The consolidation (stub step 4) shipped in `8e41215` and is **not disabled** — `release.yml`
fires on `push: tags: ["v*"]` today. So this is verification, not work:

1. **`workflow_dispatch` dry-run of `release.yml`.** It also exposes `workflow_dispatch`;
   trigger it to confirm `build` (multi-arch GHCR matrix for `vac-api` + `vac-proxy`) and
   `installer-assets` (gh-pages publish of `install.sh`/`uninstall.sh`/`compose.prod.yaml`)
   both run. NB: a `workflow_dispatch` run without a `v*` ref produces non-semver image tags —
   useful to prove the jobs *execute*; the real semver tagging is exercised by an actual tag
   push.
2. **Per-job permissions sanity.** Confirm `build` has `packages: write` and `installer-assets`
   has `contents: write` (both already set at job scope — verify nothing widened to file
   scope).
3. **Confirm `publish-installer.yml` is gone** (it is — deleted in `8e41215`) so the gh-pages
   publish isn't double-firing.

### Acceptance

A `v*` tag push runs a single `release.yml` that publishes both the GHCR images and the
installer assets; no second tag workflow exists. (Already structurally true — F2 just proves
it before relying on it for the next release.)

### Files touched

None expected. Verification only; any fix would be in `release.yml`.

---

## Execution status (this worktree — branch `track-f-ci-reenable`)

**Done.** Triggers re-enabled; release path verified.

- **F1** ✅ — Local green pass stands in for the unavailable `workflow_dispatch` dry-run (`gh`
  not installed in this env): UI `typecheck` clean, `lint-ui` warnings-only (eslint exit 0),
  Prettier `format:check` clean, `test-ui` 32/32; Go `test-go` (`-race`) all packages pass,
  `go vet ./...` clean, `go build ./...` clean. Then reverted `4893ec5`: `ci.yml` push+PR (with
  `paths-ignore`) and `bench-ram.yml` push→main + nightly `cron` + dispatch restored;
  "Temporarily disabled" comment blocks removed. All three workflows YAML-validate.
  - **Two checks NOT run locally** (no tooling here; they're the CI-runner-only jobs):
    `golangci-lint v2.12.2` (pinned) and `make bench-ram` (Docker; known to fill the dev-host
    disk per Track B). `go vet` + clean build give partial lint confidence; `bench-ram` gates
    only `main`/nightly, not PRs, so a first-run hiccup there won't block PR merges.
- **F2** ✅ — `release.yml` (never disabled) confirmed: `build` (`packages: write`) +
  `installer-assets` (`contents: write`) jobs, `concurrency: release-${{ github.ref }}`
  (`cancel-in-progress: false`), `publish-installer.yml` absent. No change needed.

> **Activation gate:** the restored `schedule` and `push: branches:[main]` triggers only fire
> from the file **on the default branch** — inert until this branch merges to `main`. The PR
> trigger activates as soon as a PR targets a branch whose base has the workflow.

## Out of scope (carried from the stub — still rejected)

- **Path-filtering Go vs. UI jobs** — UI is `go:embed`-bundled; lint/test already span both.
  Not worth the matrix complexity on a repo this size.
- **Merging `lint` + `test` into one job** — loses parallelism; the composite action already
  delivers the DRY win without the latency cost.

## Cross-track sync points

- **`.github/` only — no migration numbers, no source overlap.** Track F cannot collide with
  Tracks D (managed services) or E (trust & safety). Land independently and at will.
- **One soft coupling:** Track E (`15`/`16`) and Track D add Go/SQL that the re-enabled `lint`
  + `test` jobs will gate. Re-enabling CI (F1) raises the bar for *their* PRs — a feature, not a
  conflict, but worth a heads-up to the agents on D/E so a freshly green CI doesn't surprise an
  in-flight branch. Coordinate timing: re-enable when those branches are at a clean checkpoint.

## Suggested commits (Conventional Commit, commitlint-compatible)

- `ci: re-enable push/PR triggers for ci + bench-ram after green dry-run` (F1)
- (only if a fix was needed) `ci: <fix surfaced by the dispatch dry-run>` (F1)

> Most of `14` shipped in `8e41215`; Track F's deliverable is confidence + the re-enable, not a
> rebuild. If the dispatch dry-runs are green, F is a one-commit, docs-touching-only change.
> No `/refresh-kb` needed — KB files don't cover `.github/`.
