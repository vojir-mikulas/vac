# 17 — Installer overhaul (guided walkthrough + showcase-quality script)

**Tier:** Trust & UX · **Effort:** M · **Status:** stub

## Goal

Overhaul `scripts/install.sh` along two axes, without changing the install contract:

1. **A pleasant, guided first-run.** Walk the operator through the install with a welcome
   banner, a detected-system summary, and a few well-explained questions (domain, managed
   services, sudo-free access), ending with a confirmation summary before anything mutates the
   host. Crucially, ask whether to enable **managed services**, making clear it's off by
   default and toggleable any time via `vac managed-services on|off`.
2. **Readable, showcase-quality structure.** The script is meant to be published on the
   website for transparency, so a reader should be able to skim a `main()` of named steps and
   drill into small, single-purpose functions.

## Why it matters (strategy)

VAC's moat is simplicity + UX + reliability + trust. The installer is the *very first*
experience and (because it's published as `curl get.vac.vojir.io`) a piece of marketing.
Today it runs unattended and silently picks defaults; that's correct for automation but a
missed chance to make the operator feel in control on a hands-on install. A short, honest
walkthrough — that never changes the host until the operator confirms — reinforces the
trust posture. Publishing a clean, well-sectioned script is itself a transparency signal.

## Key constraint (the one that shapes everything)

The canonical entrypoint is `curl -sSL get.vac.vojir.io | sh`, so **stdin is the curl pipe,
not a terminal.** Interactive prompts therefore must read from **`/dev/tty`**, and must
degrade gracefully to non-interactive defaults whenever `/dev/tty` is not readable (CI,
provisioning tools, `curl | sh` without a controlling terminal). This is the single biggest
correctness risk and must be verified under the existing `sudo -E sh -c "$(cat)"`
self-elevation path.

## Aspect 1 — Guided walkthrough

### Interactivity model

- Add `prompt`/`confirm` helpers that read from `/dev/tty` (not stdin).
- Resolve `INTERACTIVE=1` once: true only if `/dev/tty` is readable **and** not forced off.
  Add a `--yes` / `--non-interactive` flag.
- **Pre-set env vars skip their prompt.** If `VAC_DOMAIN`, `VAC_MANAGED_SERVICES`, or
  `VAC_GRANT_ACCESS` are already provided, don't ask — this keeps `curl | sh` fully
  unattended for automation and preserves every current override.
- Run the wizard **after** sudo self-elevation (so `/dev/tty` is still valid) and **only on
  fresh installs** (`FRESH=1`). Upgrades skip the wizard entirely (preserve today's idempotent
  behavior). Exception: if `VAC_MANAGED_SERVICES` is absent from an existing `.env`, the
  upgrade path *may* offer that single question — otherwise leave the file untouched.

### Walkthrough flow (fresh + interactive)

1. **Welcome banner** — one-line "what VAC is", what the script will do (install Docker if
   missing, lay down `/opt/vac`, start the stack, install the `vac` CLI), and "Ctrl-C any
   time; nothing changes until you confirm."
2. **System summary** — OS/arch, Docker present/absent, install dir, detected public IP.
   Echo only, no prompt.
3. **Domain** — "Serve the dashboard over HTTPS with automatic app subdomains? (enter a
   domain, or leave blank to use the IP for now)". Note it can be set later with
   `vac set-domain`. If given, show the exact DNS records.
4. **Managed services** — *"Enable managed services (automatic backups, managed databases, an
   add-on catalog)? This starts background workers and uses a bit more RAM. Off by default —
   toggle any time with `vac managed-services on|off`. [y/N]"*. **Default: No** (matches the
   backend default and keeps the <200 MB idle claim honest).
5. **Sudo-free access** — surface the existing grant decision as a prompt (still defaulting to
   yes), showing the root-equivalent docker-group caveat.
6. **Confirmation summary** — print resolved choices (version, dir, domain, managed services
   on/off, grant) and a final "Proceed? [Y/n]" before any mutation.

### Non-interactive behavior

No `/dev/tty` → print "Running non-interactively; using defaults (managed services: off,
domain: none). Override with env vars or re-run in a terminal." Then proceed exactly as today.
This preserves the headless `curl | sh` contract.

### `.env` + wiring

- Write `VAC_MANAGED_SERVICES=<true|false>` into the generated `.env` block (alongside the
  other keys). Currently the installer never writes this key at all.
- **No backend changes needed** — `config.go` and `compose.prod.yaml` already consume it
  (`api/internal/config/config.go:318`, `compose.prod.yaml:67`). The `vac managed-services`
  subcommand already exists, so the wizard and CLI stay consistent.

## Aspect 2 — Readability / showcase quality

Restructure without changing the install contract:

- **Top-of-file doc block**: one-paragraph "what this does", the one-liner, the idempotency
  note, and an env/flag reference (single source of truth, mirrored by `usage()`).
- **Consistent section banners**, grouped: config defaults → output & prompt helpers → small
  utils (`fetch`, `rand_hex`) → preflight → wizard → docker → install dir → env generation →
  start stack → `vac` CLI → grant access → summary.
- **Wrap orchestration in `main()`** called at the bottom, so the top reads as named steps:
  `preflight`, `run_wizard`, `ensure_docker`, `lay_down_files`, `generate_env`, `start_stack`,
  `install_cli`, `grant_user_access`, `print_summary` — each a small function. This is what
  makes it pleasant to read.
- Hoist the inline blocks (docker-ensure, env-gen, setup-token wait, summary) into named
  functions — logic identical, just lifted and commented.
- Keep the embedded `vac` CLI heredoc functionally as-is; add a one-line comment pointing
  readers to it.
- Optional `step "N/7"` prefix so a guided run reads like a walkthrough.

## Invariants to preserve (non-negotiable)

- POSIX `sh`, `set -eu`, no bashisms.
- `--help` answered before any preflight/elevation.
- Self-elevation via `sudo -E` forwarding `"$@"`.
- Idempotent re-runs preserve secrets; `umask 077` + `chmod 600` on `.env`.
- All existing env overrides and flags keep working.

## Risks / verify during implementation

- `/dev/tty` availability under `sudo -E sh -c "$(cat "$0")"` self-elevation — confirm prompts
  actually read after elevation.
- The confirmation prompt must never break the unattended `curl | sh` path — gate strictly on
  `INTERACTIVE`.
- `printf`-based prompts must not assume color when not a TTY.

## Validation

- `shellcheck scripts/install.sh` (POSIX) — should be clean (good to show off).
- Manual matrix:
  - (a) `curl | sh` non-interactive → defaults, no prompts.
  - (b) `sh install.sh` in a terminal → full wizard.
  - (c) `--yes` → defaults, no prompts.
  - (d) re-run / upgrade → wizard skipped, secrets preserved.
  - (e) `VAC_DOMAIN=… VAC_MANAGED_SERVICES=true sh install.sh` → both prompts skipped.
