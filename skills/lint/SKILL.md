---
name: lint
description: "Run the FACET lint suite and fix what it reports — schema violations, @facet: tags with no record, and active records with no source anchor. Use when the user asks to check, verify, validate, or lint FACET records, when FACET CI fails, or before committing changes that touch records or anchored source."
---

# Run and fix FACET lint

```
facet-lint
```

Runs all three checks against the project. Add a path argument to lint a
different repository root.

## The three checks, and what each failure means

**`validate-schemas`** — records must satisfy their JSON Schema.
Needs `yq` (v4) and `ajv-cli`. If either is missing the check *skips* rather
than fails; say so explicitly rather than reporting a clean run, because CI
sets `FACET_STRICT_DEPS=1` and will fail there instead.

**`check-orphan-ids`** — every `@facet:` tag in source resolves to a record.
A failure means either a typo in the tag or a record that was never written.
Read the surrounding code before deciding which: if the feature genuinely
exists, the fix is a new record, not a deleted tag.

**`check-orphan-records`** — every `active` record has at least one anchor.
Either the anchor is missing (add it — see `/facet:anchoring`) or the record is
not really implemented yet (set `lifecycle: planned`). Do not resolve this by
deleting the record.

## Scope

The linter scans the whole repository by default, minus build and vendor
directories. Narrow it with `FACET_SOURCE_DIRS` (space-separated, relative to
the repo root) if the scan is slow in a large monorepo, and set the same value
in the CI workflow so both agree.

## After fixing

Re-run `facet-lint` and report the actual output. If a check skipped for
missing tooling, say which one and what to install — a skipped check is not a
passing check.
