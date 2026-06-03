# Landed

A short, append-only log of what's merged to `main`. One entry per feature/fix —
newest at the top. Keep each entry to a few lines: what changed, why it matters,
and a pointer (commit, plan, or KB file) for the detail. This is a human-readable
changelog, not the source of truth — code and `docs/kb/` win.

Format:

```
## YYYY-MM-DD — short title
One or two sentences on what landed and why. (commit `abcdef0`, plan/KB link)
```

---

## 2026-06-03 — Opt-in write-only env secrets

Env vars can now be marked **write-only**: still sealed at rest, but never
returned (reveal → 403) and non-downgradable — set/replace or delete only. An
untouched write-only secret survives the full-replace save via a `keep` path
that reuses the prior sealed bytes without decrypting, and the flag round-trips
through audit revert with no plaintext in `audit_log`. Default behaviour is
unchanged (the flag is opt-in). UI: a confirmed "Make write-only" action plus a
non-revealable row state in the env editor. (commits `a6c956a`, `553369e`;
plan `docs/plans/triage/P6-env-vars.md`; migration `00061`)
