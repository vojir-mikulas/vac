# 07 — Idle-RAM benchmark harness (defend the <200 MB claim)

**Tier:** Reliability moat · **Effort:** S–M · **Status:** stub

## Goal

Make "control plane idles under 200 MB RAM (excl. database)" a repeatable, CI-enforced
number instead of a one-time spot check.

## Why it matters (strategy)

It's a headline marketing pillar and the one MVP success criterion not verifiable by
reading code. Every subsystem added since (stats, log followers, req-metrics tailer, ws
hub) nibbles at it — without a guard it quietly rots.

## Rough shape

1. **Measure the right thing**: `vac-api` container only, not the stack. Inside the
   container read cgroup v2 `/sys/fs/cgroup/memory.current` (or `docker stats --no-stream
   vac-api`). Cross-check with `/proc/<pid>/status` `VmRSS`.
2. **Separate Go heap from total RSS**: expose `runtime.ReadMemStats` via an internal
   `expvar`/`pprof` endpoint — watch `HeapAlloc` / `Sys`. Distinguishes "our allocations"
   from "runtime overhead."
3. **`make bench-ram` target**: boot fresh → deploy 3–4 small apps (exercise log/stats/
   reqmetrics/ws) → idle 60–120s → force GC via debug endpoint → read cgroup current →
   assert `< 200 MB` and print the breakdown.
4. **CI enforcement**: run on a fixed-size runner; warn at 180 MB, hard-fail at 200 MB.
5. **Enforce at runtime too**: set `GOMEMLIMIT` (~180 MiB soft target) and a hard
   `deploy.resources.limits.memory` in `compose.prod.yaml` so a regression OOMs in testing,
   not on a user's box.

## Cheap wins to check while here

- Tune `GOGC` / `GOMEMLIMIT`.
- Audit log ring buffers / stats retention — 10k lines/service × many services adds up.

## Acceptance (sketch)

- `make bench-ram` prints a steady-state number and exits non-zero above threshold; CI runs it.
