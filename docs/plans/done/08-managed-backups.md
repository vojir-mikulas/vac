# 08 — Managed backups

**Tier:** Monetization seed · **Effort:** M · **Status:** ✅ shipped (Track D1 — `api/internal/backup`, migration `00040`)

## Goal

User-defined backup commands per service, run on a schedule, shipped to a destination.
Already designed in `mvp.md` § Backups (V2 plan).

## Why it matters (strategy)

The highest-value feature for the target user and the thing self-hosters fear most (data
loss). A natural paid-tier convenience; the open-source primitive is the foundation for
Managed VAC.

## Rough shape (from mvp.md)

- User provides a shell command run inside the container
  (e.g. `pg_dump -U $POSTGRES_USER $POSTGRES_DB`).
- VAC runs it via `docker exec` on a user-configured schedule.
- Output captured and shipped to a destination: S3 / Backblaze B2 / local volume.
- Credentials sourced from the env vars already stored in VAC — no duplicate config.
- Database-agnostic by design; **no** volume-level tarring (risks inconsistent state).

## Open questions

- Schedule model (cron expression vs. simple interval).
- Retention / rotation of backups at the destination.
- Restore flow (out of scope for v1? at least "download the latest dump").
- Encryption of backups in transit/at rest at the destination.

## Acceptance (sketch)

- A configured backup command runs on schedule, lands an artifact in the destination, and
  surfaces success/failure (+ notification on failure).
