---
name: migrate
description: Upgrade a project's FACET records to a newer schema version by running the matching migration kit. Use when the user asks to migrate, upgrade, or bump FACET or its schema version, when records declare an older schema_version than the installed spec, or when the linter rejects records for an unsupported schema version.
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Run a FACET migration

Migration kits live in `../../tools/migrations/`. Each is a
self-contained agent instruction plus a mechanical shell helper.

Edition 0.1 is the first public edition. No migration kit exists yet. If no
kit matches the requested step, report that and stop; do not invent a migration.

## 1. Work out which step applies

Read the `schema_version` of the project's records:

```
grep -h '^schema_version:' docs/facets/features/**/*.yaml | sort -u
```

Compare against the kits available in
`../../tools/migrations/` (see its `README.md` for the index).
If records are already at the newest version, say so and stop — do not run a
kit for its own sake.

## 2. Read the kit before acting

Read the kit's `AGENT.md` **in full** and follow it exactly. It is written as a
system instruction, and it defines its own pre-flight refusals — an empty
registry, a forked schema, uncommitted changes in `docs/facets/`, or an
explicit opt-out. Honour those refusals; they exist because a half-applied
migration is worse than none.

The kit's instructions take precedence over this skill wherever they differ.

## 3. Mechanical pass, then the judgement pass

Run the kit's `migrate.sh -n` first and show the user the diff it predicts.
Apply with `migrate.sh` once they agree. The helpers are idempotent — a second
run over a migrated workspace is a no-op.

Then do the part the script cannot: the per-record review the kit describes,
and the TODO list of owner-supplied answers it asks you to produce.

## 4. Do not fill owner-supplied content

Migrations that introduce new facets insert *questions*, not answers. Inserting
an empty Knowledge stub is the migration; answering it is the owner's job. Hand
back the TODO list instead.

## 5. Verify and hand off

Run `facet-lint`. Report errors and warnings separately. Do not invent
owner-supplied answers to remove Knowledge warnings.

Summarise: how many records changed, what the linter says now, and exactly what
the owner still has to answer.
