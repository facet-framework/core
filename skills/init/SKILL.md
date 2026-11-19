---
name: init
description: Set up FACET in this project — scaffold docs/facets/, install the JSON schemas, the lint suite, and the CI workflow, then tune the workflow triggers and .facetignore to the project's actual layout. Use when the user asks to adopt, add, install, initialise, or set up FACET, or asks how to start documenting features with FACET records.
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Initialise FACET in a project

Adoption has a mechanical half and a judgement half. The scaffold does the
mechanical half; you do the rest.

The scaffold exists twice and writes the same tree: `facet-init`, the plugin's
script, and `facet init`, a command of the FACET Core binary that carries
every file it writes (it needs no plugin checkout, and `npx facet-framework
init` runs it once the npm packages are published). Both take `-n` for a dry
run and `-f` to replace existing files. The steps below use `facet-init`;
`facet init` works the same wherever a Core binary with that command is on
PATH.

## 0. Offer the full setup first

This skill sets up the records layout and nothing else. `/facet:setup` walks
through everything around it as well: the binary on the shell's PATH, the
agent layer, a pre-commit lint for commits made outside Claude, private
anchors, a back-fill run, and the optional extensions. Before step 1, tell the user that
it exists, in one or two sentences, and offer to run it instead. On a yes,
hand over to `/facet:setup`. On a no, continue here unchanged.

Skip this offer when `/facet:setup` is what called this skill.

## 1. Check the ground first

Run `facet-init -n` for a dry run and read what it reports. Stop and ask the
user before continuing if:

- Records already exist under `docs/facets/features/` — the project is already
  adopted, so this is probably a repair or an upgrade. Run `/facet:migrate`
  instead if the schema version is behind.
- The repository has uncommitted changes touching `docs/facets/` or
  `tools/facet-lint/`.

## 2. Scaffold

```
facet-init
```

Existing files are kept, not overwritten. Pass `-f` only when the user
explicitly wants their copies replaced.

## 3. Check the CI workflow

`.github/workflows/facet-lint.yml` lints every pull request. It runs
`tools/facet-lint/facet`, the dispatcher the scaffold copied, which installs
the FACET version pinned in it from the public release, checksum-verified and
without a secret. The workflow has no `paths:` filter, and that is the
default to keep: anchors can sit anywhere in the tree.

If the user wants a `paths:` filter anyway, it must cover `docs/facets/**` (or
the records directory) and every directory that holds `@facet:` anchors.
Getting this wrong is silent: the workflow simply never runs. Verify with
`facet setup status`, row `ci.trigger-coverage`.

If the project keeps records somewhere other than `docs/facets/`, set
`FACET_RECORDS_DIR` in the workflow env, and mention to the user that the same
variable belongs in their shell profile so the linter agrees locally.

To move the project to another FACET version, re-run `facet-init -f` from
that version's plugin or `facet init -f` from that version's binary, or set
`FACET_VERSION` in the workflow env.

## 4. Fill in `.facetignore` — this part is yours too

The scaffold writes `.facetignore` empty, with only an explanation in it. What a
scan never needs to open is a fact about *this* repository, so decide it from
the tree rather than from a template.

Take a census before you write anything. Something like

```
git ls-files | sed 's/.*\.//' | sort | uniq -c | sort -rn | head -30
```

tells you what the repository is actually made of, and the answer is often
surprising: a game or a docs-heavy project can be a third media by file count.

**Media and documents are usually irrelevant** and are the first things to list:

- Audio, image, video, font files. They can never carry an `@facet:` anchor.
- Compiled and generated output that is versioned anyway: bundles, minified
  assets, vendored dependency trees, lockfiles.
- Prose, when the project keeps a lot of it. Note that `.md` and its relatives
  are already excluded from *claiming* anchors, since a tag in documentation
  demonstrates the syntax rather than implementing anything, but they are still
  opened and read. Listing them stops the reading too.

What stays: anything that could carry an anchor. Every source language in the
tree, configuration that is anchored, scripts. When unsure about a pattern,
leave it out — an over-eager `.facetignore` costs you anchors, an under-eager
one costs a little time.

Two things worth knowing while you decide:

- Records themselves are never at risk. They are loaded by their own directory
  walk, so no pattern here can hide a record, even `*.yaml`.
- A mistake is loud, not silent. Excluding a file that carries anchors makes
  its records orphans, and `facet-lint` reports exactly that. Run it after.

Write the patterns under the explanation already in the file, keep the
explanation, and tell the user what you excluded and why.

## 5. Propose the taxonomy — before the first record

The two upper segments of every id, `domain/capability/feature`, come from
`docs/facets/taxonomy.yaml`. Declaring them is what keeps a project from
drifting to one side of the granularity question — the failure that produces
thirty records in one repository and three thousand in another, neither of them
breaking a rule.

This is the cheapest moment to do it. Before the first record, changing the
decomposition costs nothing; after a hundred, every change costs immutable ids.

Invoke `/facet:taxonomy`, or follow
`../../docs/TAXONOMY-GUIDE.md`. In short: ask what a *user*
would name (three to twelve domains), then what the stable noun groups inside
each are (two to ten capabilities), and stop — the third level is records.

**Propose it. Do not write the file.** Which domains a product has is a product
decision, and inferring it from a directory tree puts the variance straight back
where it came from. Show the draft, say what each node is based on, name the
parts you are least sure of, and write the file only once the user accepts it.

A project may also decline a taxonomy entirely. That is supported — nothing
fires without the file — and worth mentioning rather than pressing.

## 6. Write the first record

Run `facet conventions` first. If the project already keeps a conventions
document, it says which one applies, and adoption follows it just as
authoring does. If there is none, ask whether the team wants one, for
example to fix the prose language. If they do, propose a
`FACET-CONVENTIONS.md` along the lines of "Project conventions" in
`../../docs/AUTHORING.md`. As with the taxonomy, show the
draft and write it only once it is accepted: a team's working language is
the team's decision. Skipping it is fine, and nothing changes without it.

Read `../../docs/AUTHORING.md` and follow it, or invoke
`/facet:authoring`. Prefer a feature that already exists in the codebase, so
the record can be anchored immediately and start at `lifecycle: active`.

Do not bulk-generate records for the whole codebase. FACET's adoption guidance
is explicit that back-filling belongs in a dedicated sprint with the same
review gates — one good record beats forty guessed ones.

## 7. Verify

```
facet-lint
```

Report the result to the user, and tell them what the plugin now does on its
own: records are schema-checked as they are written, `@facet:` anchors are
verified on edit, and commits are gated on the full lint suite.
