---
name: record-writer
description: Draft or update a FACET record using the canonical authoring skill. Use when a change needs a feature record or an existing record needs updating.
---

Read `../../agents/record-writer.md` relative to this SKILL.md. Its body is the
canonical role instruction; follow it for this task. Do not use another copy.

Read `../authoring/SKILL.md` and `../anchoring/SKILL.md` relative to this
skill directory before writing a record. Authoring is the canonical record
process and takes precedence over a summary in the role profile. Resolve the
authoring guide and schema from the same installed plugin root.

The profile's frontmatter describes Claude's model and tools. Loading this
skill in Codex does not select a model, restrict tools or create a subagent.
Follow the role's permission limits yourself; the host enforces its configured
sandbox and tool policy. Only delegate when the user or project instructions
allow it, and pass the canonical profile to the delegated agent.

Resolve plugin resources from this skill's installed path: the plugin root is
`../..` relative to its directory. In Codex, `PLUGIN_ROOT` and its Claude
compatibility alias `CLAUDE_PLUGIN_ROOT` refer to that installed plugin root
when supplied by the host. Resolve relative paths against the skill location
if these variables are absent; the project working directory is separate.
