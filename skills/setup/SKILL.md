---
name: setup
description: Walk a project through the whole FACET setup in one guided run — the binary on the shell's PATH, the scaffold, layout and CI triggers, taxonomy and conventions, the agent layer (hooks, MCP, LSP, status line), git (private anchors, a pre-commit lint, CI), the first records or a back-fill in one of three variants, and the optional extensions (plugins, semantic search, test runners, and a UI command where the installed build has one). Every step measures before it asks and proves its result where it takes effect. Use when the user asks to set up, onboard, install, configure, or adopt FACET end to end, asks what is still missing in a FACET setup, or names a single setup step (binary, scaffold, hooks, filter, precommit, adopt, ui).
---

# Set up FACET, step by step

This skill is an order over tools that already exist. It never re-implements
one: each step calls the command or the skill that owns it. Where a step
needs a measurement no tool makes, the tool is what is missing — say so, do
not improvise the check in prose.

## Host selection

Identify the current host before configuring the agent layer. In Codex, read
`../../docs/CODEX.md` relative to this skill directory. Invoke shared skills
as `$facet:<name>` and use the host's question tool or a direct question when
`AskUserQuestion` is unavailable. Existing user authorization applies; do not
ask again for work already requested.

Claude-specific LSP registration, status-line configuration and role model/tool
frontmatter do not configure Codex. Report those steps as not applicable in
Codex. Verify Codex skills and registry MCP in the target project, and ask the
user to review hook trust in `/hooks`; installation alone is not proof that
hooks run. Do not write Claude settings as a substitute for a Codex setup.

## Five rules for the whole run

1. **Measure first, then ask, then write.** Start every step from
   `facet setup status --json` and show what it found. A row that is already
   `ok` is reported as done and skipped. A second run is a repair, not a
   restart.
2. **Decisions belong to the user.** Taxonomy, conventions, `.facetignore`,
   the status line, the symlink directory, anything in a file someone else
   configured: show a draft, write only after a yes. Ask with
   `AskUserQuestion`, one question per decision, the recommendation first.
3. **Every step ends with a proof where it takes effect** — the proofs are
   named per step below. If a proof cannot run, the step is *unverified*,
   never *done*.
4. **Every step can be skipped and called alone.** `/facet:setup <step>`
   runs one step. A skipped step appears in the final report as skipped.
5. **Nothing leaves the machine without a yes in this run.** No push, no
   PR, no plugin from the network, no embedding call to a provider.

Probes that write (a broken record to see the hook refuse it, a commit to see
the pre-commit hook fire) run against a throwaway registry or a temporary
clone — never against the project.

## Step 0 — Explore and choose a profile

Read, do not write: git root, whether `docs/facets/` or `.facetrc.yaml`
exists, record count, schema version against the installed spec, a language
census (`git ls-files | sed 's/.*\.//' | sort | uniq -c | sort -rn | head`),
monorepo hints, whether the user can push to `origin`, the existing
`.claude/settings.json`. Then `facet setup status`.

Propose one profile and let the user confirm or change it:

| Profile | When | Steps |
|---|---|---|
| New | no `docs/facets/`, little code | 1–5, 6c, 7a, 8 |
| Back-fill | no `docs/facets/`, much code | 1–5, 6c, 7b, 8 |
| Private | FACET may not enter this repository (no write access, team has not adopted it) | 1, 2 with exclude, 3, 5, 6a, 7b |
| Repair | records exist | only the red rows; schema behind → `/facet:migrate` |
| Reader | only wants to read | 1, 8 |

## Step 1 — Binary

`facet --version` resolves and verifies the binary: it downloads the FACET
Core version the plugin pins from the `facet-framework/core` releases and
checks it against that release's `checksums.txt`. If it stops with "could not
obtain facet core", relay the options it prints: `FACET_VERSION` for another
released version, `FACET_BINARY` for a binary the user already has,
`FACET_DEV_BUILD=1` to build `./cmd/facet` from the plugin's source checkout
(needs Go), or `FACET_REPO` / `FACET_REPOS` and `FACET_RELEASE_TAG` for a fork
or mirror. Never suggest a FACET 1.x binary; the dispatcher passes it over.
Then make it reachable outside Claude, which the plugin alone does not do:

1. Detect the shell from `$SHELL` and have the user confirm it (bash, zsh,
   fish, PowerShell …). Unclear → ask.
2. `facet setup link --candidates` lists user directories already on that
   shell's PATH, per OS.
3. The user picks one. `facet setup link --dir <dir>`. The link points at
   the version-free `current` pointer, so it survives updates.
4. If the user wants a directory not on PATH, show the exact line for their
   rc file and write it **only** on an explicit yes. Never touch an rc file
   unasked.

**Proof:** `$SHELL -lc 'facet --version'` prints the same version. Without a
link, say that step 6b can then only work inside Claude.

## Step 2 — Scaffold

Follow `/facet:init` steps 1–2 (`facet-init -n`, show it, then `facet-init`;
`facet init -n` and `facet init` from the Core binary write the same tree).
When `/facet:init` is called from here, it skips its own pointer back to this
skill.

**Private profile:** after scaffolding, offer an entry in `.git/info/exclude`
for `docs/facets/`, the lint files, the workflow and `.facetrc.yaml`. Show
the lines, write on a yes. The repository changes by zero bytes.
*Proof:* `git status --porcelain` names none of them, `facet lint` still
finds the records.

**Proof:** `FACET_STRICT_DEPS=1 facet-lint` checks more than zero records.

## Step 3 — Layout

- Monorepo signs → `/facet:registries`.
- `.facetignore` from the census → `/facet:init` step 4.
- CI workflow → `/facet:init` step 3. It has no `paths:` filter; keep it
  that way unless the user asks for one.

**Proof:** `facet setup status` row `ci.trigger-coverage` is `ok` — no
`paths:` filter, or every pattern matches at least one tracked file.

## Step 4 — Taxonomy and conventions

`/facet:taxonomy` and `facet conventions`, both as **proposals**. Declining
is a valid outcome and is reported as such.

## Step 5 — Agent layer

The plugin already ships the hooks, the `registry` MCP server and the
language server. This step makes them visible and proves they answer.

`facet setup status` proves `mcp` and `lsp` itself. It cannot see whether
Claude Code runs the plugin's hooks, so its `hooks.plugin` row is always
`unmeasured` — the proof for the hooks is yours, with real hook JSON on
stdin against a **throwaway registry**, never the project:

```sh
T=$(mktemp -d); git init -q "$T"; mkdir -p "$T/docs/facets/features/demo/thing" "$T/src"
# a valid record, an anchor naming it, and one naming nothing
```

Write a minimal valid record `demo/thing/widget` (schema version from
`facet setup status`'s `scaffold` row), `src/w.ts` anchoring it, and commit
it with `--no-verify`. Then, each with its own payload:

| Hook | Payload and action | Proof |
|---|---|---|
| SessionStart | `{"session_id":"probe","cwd":"$T"}` → `facet hook session-start` | output names the record count |
| Write/Edit check | change the record's `id`, payload `{"tool_name":"Write","tool_input":{"file_path":"$T/docs/…/widget.yaml"},"cwd":"$T"}` → `facet hook post-tool-use` | it names the problem (id does not match the path) |
| Anchor check | `src/g.ts` with `@facet: demo/thing/ghost`, payload for that file → `facet hook post-tool-use` | it names `demo/thing/ghost` |
| Commit gate | stage `src/g.ts`, payload `{"tool_name":"Bash","tool_input":{"command":"git commit -m x"},"cwd":"$T"}` → `facet hook pre-tool-use` | `"permissionDecision":"deny"` |

Delete `$T` afterwards. These four prove the binary answers the events;
that Claude Code actually delivers them is shown by this session itself —
if records were checked as you wrote them during this run, say so; if not,
the hooks row stays *unverified*.

- **Turning a gate off** is a committed change to `.facetrc.yaml`
  (`hooks: { commit_gate: off }`), read from HEAD. The gate refuses a commit
  that switches itself off, so this is a decision for the person running
  the session, committed from their terminal or with
  `FACET_SKIP_COMMIT_GATE=1` in the environment Claude was started with —
  never something you do. The switch applies only to a plain `git commit`
  in that repository. Ask whether anyone wants a gate off and name the
  cost; do not suggest it.
- **Status line** → `/facet:statusline`, with its rule: compose with an
  existing line, never replace it.

## Step 6 — Git

**6a Private anchors** — `facet filter install`, offered in the Private
profile or on request, never by default. *Proof:* in a scratch copy, an
added anchor is absent from `git diff --cached` after `git add`, present in
the working copy, and `facet filter status` reports no orphans. The
install serves every worktree of the clone, so repeat the probe in a second
worktree (`git worktree add` in a scratch location) — that is the claim to
measure. Undo the probe and remove the worktree.

**6b Lint on every commit, not only Claude's** — the plugin's commit gate
only sees commits made through Claude. `facet setup precommit` shows what it
would install; `--write` installs a `pre-commit` hook running
`facet lint --staged`. With husky, lefthook or pre-commit present it prints
the snippet for that manager instead of writing a hook. Requires step 1's
link; without it, report 6b as not possible and why.
*Proof:* in a temporary clone, a commit with a broken staged record is
refused, a clean one passes, and a broken record that is **not** staged
does not block a clean commit.

The rule behind it: **anything that runs on its own lints the staged state
only** and reports only errors the commit introduces; `facet lint` run by
hand lints the whole repository.

**6c CI** — the workflow from step 2. Locally only the trigger proof from
step 3; that it runs on the forge is visible after a push, and say so.

## Step 7 — Content

**7a New:** `/facet:authoring` for one existing feature, so it starts
`active`. No bulk generation.

**7b Back-fill:** records for code that already exists, written with the
skills this plugin ships. Clarify items 1 and 2 below first (where the notes
of the run live, and the languages), then work area by area:

- `/facet:taxonomy` proposes the domains and capabilities for the area from
  the code, and the user confirms them before any record is written.
- `/facet:authoring` writes one record per capability's features, from what
  the code and its tests show; a behaviour no test covers gets a scenario
  and no `verification` entry, never an invented one.
- `/facet:anchoring` places the `@facet:` anchors, and a record turns
  `active` only once its anchor is in the source.
- `facet lint` after each area; `/facet:drift-auditor` reviews the area
  read-only before the next one starts.

One area per session keeps the run reviewable; say how many areas there
are and propose an order (most-changed code first).

*Optional accelerator.* If your plugin installation contains the
`facet-adopt` workflow (`workflows/adopt.js` under the plugin root; check,
do not assume), it can run the same back-fill in bulk. Use it only after the
pre-clarification below, every item recorded; without it, items 3 and 4 and
the rest of this step do not apply:

1. **Where the run protocol lives.** In the repository and versioned
   (`docs/facets/adopt/<date>-<area>/`), in the repository but excluded via
   `.git/info/exclude` (mandatory in the Private profile), or a path outside
   the repository. The protocol is where an aborted run resumes.
2. **Languages — three separate questions:** the Gherkin scenarios
   (`Given`/`When`/`Then` stay as keywords), the prose of the records, and
   the answers to the owner's open questions. If `facet conventions` names
   a conventions document, show what it declares and have it confirmed
   instead of asking again; otherwise offer to record the answers there.
3. **Scope and cost.** On a large tree, propose one area first and pass
   it as `scope` — to the estimate and to the run, the same value. Run the
   workflow with `estimateOnly: true` and show the estimate for each
   variant side by side, including its named assumptions. The real run
   surveys again, so the capability list can differ slightly from the one
   priced; say so. Say plainly:
   > On a large repository this run uses a very large number of tokens and
   > can use up the weekly allowance. Starting it shortly before the weekly
   > reset is recommended, so an abort does not cost the rest of the week.

   When the reset is depends on the coding agent and the user's
   subscription. You do not know it and do not guess or ask — tell the user
   where to look (in Claude Code usually `/status`).
4. **Variant**, presented as a trade-off between tokens and reliability:

| | 1 — Quick | 2 — Thorough | 3 — Evidenced |
|---|---|---|---|
| Tokens | low | clearly higher | very high |
| Quality | low; on complex software expect many errors | good | very good |
| A finding means | nothing reliable — usually the record is wrong | a pointer: deviations and missing tests a human must check | a statement backed by a run in the product; probably a real fix |

   Say it explicitly when offering them: quick produces drafts, not
   finished records; thorough ends with a checklist, not a verdict;
   evidenced reports bugs as bugs because they were observed.
   **Evidenced without a startable test environment** is still offered,
   with two notices before the start and in the report: the tests should
   then be done manually (the workflow writes a test instruction per
   deviation), and every finding resting on code review alone is labelled
   a possible false positive and kept out of the bug list.
   For evidenced with an environment, ask for start command, URL and the
   *name* of the variable holding test credentials — never the credential.

Start only after a yes. Pass the answers as `variant`, `scope`,
`protocolDir`, `languages`, `models` (default: sonnet drafts and revises,
opus reviews) and `app`.

If the run stops with a reason (`protocol-load-failed`, `protocol-corrupt`,
`languages-undecided` …), report the reason as given — do not restart from
scratch: the protocol is where it resumes. For evidenced without a product,
tell the user the manual test instructions are in the protocol and that an
observation they write under one (`Observed: …`) is what turns that
deviation into a finding on the next run.

At the end, **ask whether the protocol is deleted or kept**, and say what
deleting loses: resuming the run, and for evidenced the test records behind
each finding. Delete nothing and keep nothing unasked.

**Proof:** `facet lint` green; every active record has an anchor
(`anchor_list` over MCP, not the workflow's return value).

## Step 8 — Read and extend

One question each, all optional:

| Part | Tool | Proof |
|---|---|---|
| UI command | only where `facet --help` lists a `ui` command (not in Core): run it | the address it prints answers |
| Plugins | `facet plugin list`; add only on an explicit request with a source | `list` shows it with its grant |
| Wiki | only where `facet --help` lists the wiki (not in Core): its `new` command or the Confluence import | its `check` command green |
| Semantic search | `facet rag index`; the key only from an environment variable — ask for the variable's name, never the key | `facet rag query` returns hits |
| Test runners | `facet test` with a runner from `.facetrc.yaml` | scenarios reported as measured |

## Step 9 — Close

Run `facet setup status` again and report in four groups: **done and
proven · done, unverified (why) · skipped · not possible (why)**. List the
files changed and propose a commit — commit only on a yes.
