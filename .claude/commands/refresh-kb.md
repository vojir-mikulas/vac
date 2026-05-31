---
description: Regenerate docs/kb/ from current source and refresh provenance headers
argument-hint: "[optional: a single kb filename, e.g. deployment-flow.md]"
---

Regenerate the knowledge base in `docs/kb/` so it matches the **current source code**.

## Core rule — regenerate from source, never patch the summary

The previous content of each KB file is a hint about *what to cover*, NOT a source of truth.
Re-derive every fact from the actual code. If the code and the existing doc disagree, the code
is right and the doc is what's being fixed. Do not copy claims forward without re-verifying
them against the files — that's how AI-maintained docs silently drift.

## Steps

1. Determine provenance: get the current short commit and date.
   - `git rev-parse --short HEAD`
   - `git log -1 --format=%cs`
2. Decide scope: if `$ARGUMENTS` names a specific file, regenerate only that one. Otherwise
   regenerate all files in `docs/kb/`.
3. For each KB file, in order:
   - Read the existing file to see its scope and structure (headings, what it documents).
   - Re-derive its content from source. Use the Explore agent for breadth (e.g. "trace the
     deploy pipeline in api/internal/deploy and report real package/function names"). Read the
     actual packages it documents — for example:
     - `architecture.md` → `api/internal/` package list, `ui/src/` layout, `store/` tables,
       `docs/deviations.md` for invariants.
     - `deployment-flow.md` → `deploy/`, `compose/`, `dockercli/`, `proxy/`, `caddy/`,
       `server/handler/deployments.go`, `deploy/status.go`.
     - `conventions.md` → the real directory layout of `api/internal/`, `server/handler/`,
       `store/`, and `ui/src/`.
   - Rewrite the file to reflect current reality. Keep it concise and structural — document
     packages, functions, flows, and invariants. **Do not add line numbers** (they drift; the
     symbol name is enough to grep).
   - Update the provenance header (the leading HTML comment) to the current commit and date,
     keeping the "regenerate with /refresh-kb / treat as stale if HEAD moved past this and X
     changed" wording.
4. After writing, report a short diff summary per file: what changed vs. the prior version
   (new/removed packages, renamed functions, changed flow, invariant added/removed), or
   "no substantive change — only provenance bumped" if nothing moved.
5. If you discover the code now contradicts something in `docs/plans/` or `docs/deviations.md`
   in a way worth recording, mention it — but don't edit those here (plans are historical;
   deviations are a deliberate human log).

Keep each KB file tight. These are a map, not a mirror of the code.
