---
name: catch-up
description: Review and reconcile FACET records after a pull, merge, rebase or branch switch, using the last accepted review baseline rather than HEAD.
---

# Catch up after incoming changes

Read the project's configuration and conventions. Run `facet drift --json`
from the registry workspace before deciding which records need review.

## Establish the review window

Check `baseline.status` first; when `repositories` is present, check every
entry's baseline individually. If a comparison status is not `ok`, explain the
failure and resolve it before claiming the review complete. Treat `unreadable`
as missing evidence, not a clean result.

For `kind: accepted` or `kind: explicit`, compare against `baseline.commit`.
For `kind: head`, a clean result covers only uncommitted work. Ask the user for
the last reviewed revision before judging incoming commits. `ORIG_HEAD` may be
offered as a candidate, but never assume it: another git operation can replace
it. Then rerun with `--baseline <confirmed-ref>`; for a SHA in an independent
source clone, also use `--repository <workspace-relative-repo>`.

Independent source clones must be declared in `.facetrc.yaml` `sources`, such
as `sources: [platform/backend]`, even if the outer repository ignores them.
Each clone has its own baseline and its own local review ref. Do not infer one
clone's review from another, or scan undeclared ignored clones automatically.

## Review and reconcile

In each source repository compare the working tree against its baseline commit
(`git -C <repo> diff <commit> -- <path>`). Include committed, staged, unstaged
and untracked changes; read untracked files separately. Use the canonical
auditor method bundled with this skill (`../../agents/drift-auditor.md`), applying it in the current agent or handing
it to an available auditor when delegation is authorized. The auditor reports
only; it never accepts a review.

Review the ids in `drift`, `referenceChanges` and `staleReferences`. Deletions
and removed anchors need the baseline's evidence even when no source remains.
For clones using private anchors, run `facet filter status` in each clone.
Distinguish missing local anchors, anchors originating in other worktrees and
legacy anchors whose origin is unknown. Inspect missing anchors in changed
files and restore a justified placement; keep their shared sidecar entries.

Update records whose observable behavior changed using the authoring skill,
and repair affected references and anchors. Draft supported Knowledge answers
and identify invalidated owner answers and any missing owner input for review.
Run the relevant tests and FACET lint before concluding reconciliation.

## Record the completed review

Accept only content actually reviewed and reconciled. For a partial review name
only the reviewed workspace-relative paths:

```sh
facet drift accept --repository platform/backend --outcome record-updated platform/backend/src/pay.go
```

When the review used an explicit confirmed revision (especially because no
accepted baseline existed), pass that same revision as `accept --baseline
<confirmed-ref>`. Without it, a first partial acceptance starts from current
HEAD and would also clear incoming committed changes you did not review.
Keep `--repository` when the revision belongs to a source clone. Later partial
acceptances normally build on the newly accepted snapshot; do not reuse the
older explicit revision unless you intend to restart that review window.

Use `behavior-unchanged` when no record update was needed, or
`references-repaired` when only references were repaired. With no paths the
command accepts every change in the selected repository (every declared source
repository when none is selected), so use that form only after the whole scope
was reviewed. Acceptance is a local snapshot, not human approval of drafts.

Rerun `facet drift --json` and inspect every repository's baseline and remaining
findings. Report what was reviewed, the baseline used, changes made, tests run
and any remaining unreviewed paths or owner questions. A HEAD-only comparison,
failed comparison or unresolved finding is not a completed catch-up.
