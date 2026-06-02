# 00 — Parallel execution tracks (triage items)

How to run the [triage](README.md) fixes & features as **concurrent tracks**. Same rule as
[`../upcoming/00-parallel-tracks.md`](../upcoming/00-parallel-tracks.md): each track owns a
distinct slice of the codebase so tracks rarely touch the same files. Items **within** a track
are sequenced (shared files / build on each other); items **across** tracks run in parallel.

Unlike the upcoming tracks, most of this is **bug-fix + UX-surfacing**, not greenfield — so the
collisions are concentrated in a few shared files (the app DTO, the services handler, the app
Settings UI, the Grafana addon template). Those are called out as **sync points** below; resolve
them with one shared seam up front and you can run 6 agents in parallel.

```
  Stage 0 (shared seam): expose app source/template + Source/Stack on the app DTO
        │
        ├─ Track P1: ADDONS & MANAGED SERVICES ── DB-collision(BUG) → addon-distinction → mariadb → uninstall → db-tab/backups/icons
        │
        ├─ Track P2: DEPLOY & BUILD PLUMBING ───── port-redeploy(BUG) → 3000-collision(BUG) → image-prune → deploy-retention
        │
        ├─ Track P3: APP-DETAIL UX ─────────────── header-controls ‖ per-service stop+logs → overview-panel → container-shell
        │
        ├─ Track P4: DOMAINS ───────────────────── base-domain-display(BUG) → per-app-domains
        │
        ├─ Track P5: SECURITY & METRICS ────────── honest-status → privileged-helper ‖ metrics-disabled-msg ‖ badge
        │
        └─ Track P6: ENV VARS ──────────────────── write-only-secret toggle   (isolated)
```

> **Migrations:** latest applied is `00042`; the upcoming Track E reserved `00050–00059`. **Any
> migration added by triage work must start at `00060`+** to avoid clashing. Only P6 (and maybe
> P1's per-DB binding name) need one.

---

## Stage 0 — shared seam (do FIRST; unblocks P1 + P3)

**One small PR before the addon/overview work starts.** Today the app DTO
(`server/handler/apps.go` `appDTO`, ~line 68) omits `source` and `template_id`, so the UI can't
tell a Grafana addon from a git app — this is the root cause behind several P1 items **and** the
P3 overview panel needs the same fields.

- Expose on the app DTO: `source` (`git`/`template`), `template_id` + resolved template name &
  icon, and the Source/Stack fields the overview panel wants (repo, branch, commit, framework,
  build_kind, service count, RAM cap, network).
- No schema change — these columns already exist (`store/apps.go`); this is DTO plumbing only.

Land this, then P1 and P3 proceed without fighting over `apps.go`.

---

## Track P1 — Addons & Managed Services *(sequential; greenfield-ish)*

**Owns:** `internal/addon`, `internal/dbprovision`, `internal/backup`, the
databases/backups/addons handlers, and the addon-catalog + app-tabs + app-Settings UI.
**Plan files:** [addons.md](addons.md), [databases-and-backups.md](databases-and-backups.md).

| Order | Item | Effort | Note |
|---|---|---|---|
| P1.1 | **`DATABASE_URL` collision** (BUG) | M | Two managed DBs overwrite each other — unique per-DB env name. *(may need migration `00060`+ for a binding-name column)* |
| P1.2 | Addon distinction UI | S | After **Stage 0**: installed status, hide DB/Backups/Build tabs, "Installed from {template}" Settings |
| P1.3 | MariaDB addon catalog entry | S–M | Reuse addon→provision path; **edits the addon registry — sync with P2.2 on the Grafana template** |
| P1.4 | Uninstall addon | M | Addon-aware teardown (app + provisioned DB/volumes) + confirm |
| P1.5 | Database tab + backup UX + brand icons | M | Surface existing `UpdateBackup`/`DownloadBackup`/S3; add icons (react-icons) |

**Why sequential:** all share `addon`/`dbprovision`/`backup` and the same app-tabs/Settings UI.

## Track P2 — Deploy & Build plumbing *(sequential; critical path)*

**Owns:** `deploy/pipeline.go`, `internal/dockercli`, `internal/retention/pruner.go`, `config`,
and the **port** write-path in `server/handler/services.go` + `store/services.go`.
**Plan files:** [port-handling.md](port-handling.md), [build-cache-and-retention.md](build-cache-and-retention.md).

| Order | Item | Effort | Note |
|---|---|---|---|
| P2.1 | **Port change → redeploy** (BUG) | M | `SetServiceConfig` is DB-only today; make a port change actually redeploy + refresh Caddy upstream |
| P2.2 | **3000 collision** (BUG) | S | Move vac-api's default off 3000 and/or fix the Grafana template port — **sync with P1.3 on the addon template** |
| P2.3 | Image prune wiring | M | `ImageKeepCount`/`ListImages`/`RemoveImage` exist but are **never called** — call from the retention pruner |
| P2.4 | Deployment retention | M | Add to the same pruner; keep enough history for `internal/revert` |

**Why sequential:** P2.1 and P2.3/P2.4 both touch the deploy/up + dockercli path.

## Track P3 — App-detail UX *(mostly parallel internally)*

**Owns:** the app-detail UI, the per-service **action** path in `server/handler/services.go`, and
a new exec/PTY endpoint. **Plan file:** [app-detail-ux.md](app-detail-ux.md).

| Order | Item | Effort | Note |
|---|---|---|---|
| P3.1 | Header start/stop/restart-all | S | Fan out to existing per-service actions |
| P3.2 | Per-service Stop + View logs | S | Adds actions next to Restart — **same file as P2.1 (`services.go`); see sync points** |
| P3.3 | Overview panel (Source + Stack) | M | After **Stage 0**; addon apps show "from addon" |
| P3.4 | Container shell (WS PTY) | L | New, privileged → gate + audit-log; could graduate to its own `../upcoming/` stub |

## Track P4 — Domains *(parallel)*

**Owns:** domains handler / `instance.go` base-domain path, the Settings→Domains UI, and a new
per-app Domains section. **Plan file:** [domains.md](domains.md).

| Order | Item | Effort | Note |
|---|---|---|---|
| P4.1 | Base-domain display (BUG?) | S | Card should pre-fill the **effective** value + source; verify GET returns it. No restart needed (reconciles live) |
| P4.2 | Per-app Domains in app Settings | M | API exists (`ListAppDomains`/`AddCustomDomain`) — **adds a section to app Settings UI; sync with P1.2** |

## Track P5 — Security & Metrics *(parallel; mostly disjoint)*

**Owns:** `internal/security`, `internal/reqmetrics`, the security handler + UI, optional
privileged helper. Relates to [`../upcoming/15-security-dashboard.md`](../upcoming/15-security-dashboard.md).
**Plan file:** [security-and-metrics.md](security-and-metrics.md).

| Order | Item | Effort | Note |
|---|---|---|---|
| P5.1 | Honest status states | S | not-installed vs not-readable(needs-helper) vs disabled vs healthy — never a bare "failing" |
| P5.2 | Opt-in privileged read helper | M | So fail2ban/ufw panels can populate despite the sandbox |
| P5.3 | Request-metrics disabled message | S | Say when `VAC_SECURITY_MONITOR` is off vs genuinely idle |
| P5.4 | Security nav badge count | S | Red badge = count of failing posture checks |

## Track P6 — Env vars *(parallel; isolated)*

**Owns:** env handler/store/UI only. **Plan file:** [env-vars.md](env-vars.md).

| Order | Item | Effort | Note |
|---|---|---|---|
| P6.1 | Optional write-only secret toggle | S | Migration `00060`+ for the flag; default behavior unchanged (masked + audit-logged reveal) |

---

## Cross-track sync points (the only places parallel work bites)

1. **App DTO (`apps.go`)** — needed by P1 (addon distinction) **and** P3 (overview panel).
   → resolved by **Stage 0** up front; don't let two tracks edit `appDTO` independently.
2. **`server/handler/services.go`** — P2.1 edits the **port write-path**; P3.2 adds **stop/logs
   actions**. Different functions, same file. → land P2.1 first or coordinate the merge.
3. **App Settings UI** — P1.2 adds an "Installed from {template}" panel; P4.2 adds a per-app
   Domains section. Additive sections in the same screen. → coordinate component layout.
4. **Grafana addon template** — P1.3 (MariaDB/icon edits to the addon registry) and P2.2 (Grafana
   port) both touch addon templates. → one owner for template edits, or sequence P2.2 → P1.3.
5. **Migrations** — claim **`00060`+** (P6 definitely; P1.1 maybe). One numbered sequence.

## Staffing guide

- **Fix the bugs first, regardless of track** (these are confirmed defects, highest value):
  **P2.1** port-redeploy, **P1.1** `DATABASE_URL` collision, **P2.2** 3000 collision, **P4.1**
  base-domain display.
- **1 person:** Stage 0 → the four bugs above → then pick by leverage (P1 addon distinction is
  cheap and high-visibility; P5 honesty fixes the "everything's failing" feeling). Ignore the
  track structure — it's for parallelism you don't have yet.
- **Parallel agents (after Stage 0):** P1 ‖ P2 ‖ P3 ‖ P4 ‖ P5 ‖ P6 run concurrently. P5 and P6
  are the most isolated (safest to hand to an agent). Honor the 5 sync points above; start any
  new migration at `00060`+.

## The critical path

Stage 0 is the unlock. After it, the **four confirmed bugs** (P2.1, P1.1, P2.2, P4.1) are the
spine — they're the things that are actually broken when you use VAC. Everything else is
UX-surfacing of capabilities that already exist or net-new polish, and can land in any order.
</content>
