---
name: drift-auditor
description: Audit changes for drift between FACET records and implementation. Use for a read-only review of changed source and records.
---

Read `../../agents/drift-auditor.md` relative to this SKILL.md. Its body is the
canonical role instruction; follow it for this task. Do not use another copy.

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

## When a Stop-hook notice resumes the original task

The audit verdict is input to the main agent's completion message. After the
review, the main agent checks `facet drift --json` in the affected repository
or worktree named in the notice and sends a complete final response to the
original task again. If the binary is unavailable, report the missing status
check and review the listed files and records without claiming a clean status.
Lead with the task result; retain relevant deliverables, links, verification
and remaining limitations. Correct claims invalidated by the review or later
edits, and briefly append the FACET outcome, including unresolved findings or
a failed status check. A deduplicated notice does not establish a clean state.
Do not end with only a drift acknowledgement or send the user back to an
earlier message. Unrelated changes do not authorize edits or baseline acceptance.

When delegated only the audit, return the scoped verdict to the parent; the
parent restores the original task's completion message. Keep this role read-only.
