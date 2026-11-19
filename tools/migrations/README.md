# FACET migrations

Edition 0.1 is the first public edition. No migration exists yet.

## Kit format

A future migration kit lives at `tools/migrations/<from>-to-<to>/` and contains:

- **`AGENT.md`**: self-contained instructions for an agent working in a consumer project. Read it first.
- **`migrate.sh`**: an idempotent POSIX shell helper for mechanical changes, with `-n` for a dry run.
- **`REPORT.md`**: a per-record follow-up list generated during a run, never committed as part of the kit.

A release needs a kit when consumer records or vendored tooling must change.
The kit states its supported input and output versions, pre-flight checks,
owner decisions, verification and repeat-run behavior. It must preserve
owner-supplied content. Plugin installation updates are separate from record
format changes.

The [migrate skill](../../skills/migrate/SKILL.md) selects a matching kit,
reads its instructions and verifies the result. Without a matching kit it
reports the missing migration instead of changing records.
