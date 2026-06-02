# Env vars — should encrypted values be recoverable?

**Status:** triage · **Effort:** S · Mostly a design decision

The note: *"Should encrypted env vars be recoverable? Right now I can just preview them again
and copy them out."*

## Reality check

Env vars are encrypted at rest (sealed with the master key) regardless of the flag. The
`sensitive` flag controls **display**, not encryption:
- `ListAppEnv` (`server/handler/env.go`) returns non-sensitive values decrypted, but **omits
  the value entirely** for `sensitive=true` keys (the DTO drops `Value`).
- Revealing a sensitive value requires an explicit, separate call: `RevealAppEnv`
  (`server/handler/env.go:60`), and that reveal is **audit-logged** ("env var revealed").

So sensitive values are *not* shown in the list or copyable by default — they're masked, and
reveal is a deliberate, logged action. The current behavior is reasonable.

## Decision to make

The question is policy, not a bug:
- **Keep recoverable (current):** an operator who set the secret can re-reveal it (audit-logged).
  Pragmatic for a single-operator box — you'll need the value to debug/migrate.
- **Add a write-only mode:** optionally mark a secret as **never-revealable** — you can set/replace
  it but never read it back (no `RevealAppEnv` for those keys). Matches "secrets should be
  write-only" expectations.

**Suggestion:** keep reveal as default (it's audit-logged), but add an optional per-secret
"write-only / no reveal" toggle for users who want true one-way secrets. **S**

## Acceptance sketch

- Default unchanged: sensitive values masked, reveal is explicit + audit-logged.
- Optional: a secret can be marked write-only so even reveal is refused.
</content>
