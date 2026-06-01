# Working with AI agents safely (worktrees & branch isolation)

How to direct Claude (or any AI agent) to implement a feature without leaking
changes into `main`. The recurring failure mode: an agent — often a spawned
**subagent** — forgets to switch branches and edits/commits on `main`. The fix
is to make isolation **structural** so even a forgetful agent physically can't
touch `main`, rather than relying on it to remember.

Mechanisms below run from most to least robust. The recommended setup is to
combine #1 (worktree) and #4 (mechanical guard).

## 1. Put the agent in a worktree *before* asking for the feature (best)

A git worktree is a separate working directory checked out to its own branch. If
the whole session is rooted in a worktree, every subagent inherits that working
directory — there's no `main` checkout for them to leak into. Subagents run in
the **same working directory** as the parent, so rooting the parent in a
worktree contains them automatically.

Using the harness worktree command:

```
/worktree            # (or whatever exposes EnterWorktree)
→ creates ../vac-<feature> on branch feature/<x>
then: "implement <feature>"
```

Or do it yourself first and launch the agent from inside it:

```bash
git worktree add ../vac-feature-x -b feature/x
cd ../vac-feature-x
# start claude here
```

Clean up when the branch is merged/abandoned:

```bash
git worktree remove ../vac-feature-x
```

## 2. For parallel/risky subagent work, use per-agent worktree isolation

When delegating to a subagent, the agent can be spawned with worktree
**isolation**, giving *that agent* its own throwaway git worktree (auto-cleaned
if it changes nothing). Trigger it by saying so explicitly:

> "Do this in an isolated worktree, don't touch main."

Good when multiple agents edit files concurrently and would otherwise collide.

## 3. State the branch contract up front, explicitly

Agents "forget" the branch mostly when the branch was never made explicit. Phrase
the request as a hard precondition with a checkable gate:

> "First create and switch to branch `feat/x` (or a worktree). Verify with
> `git branch --show-current` before any edit. Do **not** commit to `main`. If
> you find yourself on `main`, stop and tell me."

Naming the verification step turns "remember to switch" into a checkable gate.

## 4. Make `main` mechanically unwriteable (belt-and-suspenders)

Independent of what any agent intends:

- **Local git guard** — a `pre-commit` hook that aborts if the current branch is
  `main`:

  ```bash
  # .git/hooks/pre-commit  (or a tracked hooks dir)
  #!/bin/sh
  if [ "$(git branch --show-current)" = "main" ]; then
    echo "Refusing to commit on main. Switch to a feature branch/worktree." >&2
    exit 1
  fi
  ```

- **Harness guard** — a `PreToolUse` hook in `.claude/settings.json` that blocks
  `Edit`/`Write`/`git commit` when the current branch is `main`. This catches
  *every* agent and subagent regardless of whether they remembered, because the
  harness enforces it, not the model.

## Recommended setup

Combine **#1 and #4**: always launch feature work from a worktree (so subagents
are contained by construction), and add the `PreToolUse`/pre-commit guard so a
stray write to `main` is impossible rather than merely discouraged. The explicit
branch contract from **#3** is good hygiene on top.
