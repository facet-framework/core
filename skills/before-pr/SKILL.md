---
name: before-pr
description: "Check FACET record drift locally before opening a pull request: compare the branch with its merge base on the target branch, settle every finding in the branch, and only then open the PR. Use before `gh pr create`, before pushing a branch for review, when asked whether a branch is ready for a PR, or when an agent finishes a change that touched anchored source."
---

# Drift check before a pull request

Run this in the branch's own worktree, after the last code change and before
the PR is opened. It replaces nothing in CI; it is the step that keeps a
merged change from arriving on the target branch with records that did not
follow.

## 1. Compare against the branch's own window

Fetch the target branch and take the merge base, so the window is exactly
what this branch adds — not what other people merged since, and not this
worktree's accepted review baseline (that one belongs to `/facet:catch-up`):

```sh
git fetch origin <target>
base=$(git merge-base "origin/<target>" HEAD)
facet drift --baseline "$base" --json
```

Run it from the registry workspace. Check `baseline.status` first, and every
entry under `repositories` when present; a status other than `ok` is not a
clean result. Commit or stash nothing for this — the comparison covers
committed, staged, unstaged and untracked changes.

## 2. Settle every finding in the branch

For each id in `drift`, read the record and `git diff "$base" -- <path>`:

- **Behaviour changed** — update the record in this branch with
  `/facet:authoring`: intent, scenarios, verification ids, invalidated
  Knowledge. A renamed or translated test title is a changed verification id.
- **Behaviour unchanged** (refactor, comments, formatting, a shared file whose
  change concerns another record) — no record edit; write one line for the PR
  body: `<record id> — unchanged: <what changed instead>`.

Repair every entry in `referenceChanges` and `staleReferences`; a deletion or
a removed anchor needs the evidence from `$base`. Do not answer owner-only
Knowledge slots — name them as open questions in the PR body.

Then rerun `facet drift --baseline "$base"` and the FACET lint. Every
remaining finding must have either a record change in the branch or a line
for the PR body.

## 3. Open the pull request

Add a section to the PR body listing the outcome per record: updated, or
unchanged with its one-line reason. Do not run `facet drift accept` for this
check — acceptance records a worktree's local review after a pull, not a
branch's readiness.
