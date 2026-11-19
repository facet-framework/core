---
name: registries
description: "Configure a project's FACET layout in .facetrc.yaml, or set up several registries in a monorepo that holds more than one product. Use when records live somewhere other than docs/facets, when the user asks how to document a monorepo with several apps, when the linter reports a registry problem, or when an @facet: tag needs a registry prefix."
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Registries: where FACET looks, and how many products it documents

The canonical guide is `../../docs/REGISTRIES-GUIDE.md`, and the
spec section is §5.4. Read the guide before changing a layout.

## Two separate jobs

**Job one — say where things are.** Almost every project needs at most this:

```yaml
# .facetrc.yaml at the repository root
records: docs/facets
```

Add `sources:` when only part of the repository carries anchors, and `schema:`
when the JSON schemas are somewhere unusual. Omit the file entirely and the
defaults apply, which is correct for most projects.

For a registry documenting independent Git clones under the workspace, declare
each clone (or a directory inside it) in `sources`, including clones ignored by
the outer repo. An omitted clone has no scanned anchors. Drift compares each
declared source repository against its own review baseline; `--repository`
selects one for clone-specific revisions, acceptance or reset. Source paths and
review path arguments stay workspace-relative. See the guide's independent
source clones section and use the catch-up skill after incoming commits.

If the user is currently setting `FACET_RECORDS_DIR` or `FACET_SOURCE_DIRS`
permanently — in a shell profile, a Makefile, a CI workflow — that value belongs
in this file instead. The environment still wins where it is set, which is what
makes a forgotten export so hard to notice.

**Job two — several registries.** Only for a repository holding more than one
*product*. This is the part to be careful about.

## Settle registries-versus-variants first

Ask one question: **"does this feature exist over there yet?"**

- The question is meaningful → **variants**, one registry. Web and native,
  legacy and rewrite, EU and US deployment. Use `/facet:variants`.
- The question is meaningless → **registries**. A storefront and an internal
  admin tool; a product and its marketing site.

Do not infer this from the directory layout. A rewrite lives in directories just
as separate as two products do, and modelling it as two registries duplicates
every intent and contract — two normative statements of one behaviour with no
tie-break when they diverge. If the user asks for a second registry and the
answer is variants, say so and propose variants instead; do not just comply.

(The mistake is recoverable rather than fatal: because ids are global, a split
rewrite still answers "which features has v2 not reached yet" via
`facet registries`. You pay in duplicated prose, not in a lost answer. Say that
too — it is the difference between a warning and a veto.)

## Setting several up

```yaml
schema_version: "0.1"

registries:
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront, packages/shared]
    description: the customer-facing shop
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin, packages/shared]
    description: the internal back office
```

- Names follow the id grammar (`^[a-z][a-z0-9-]*$`) — they are written in front
  of ids.
- `registries` cannot coexist with a top-level `records`/`sources`/`schema`.
- Each `records` directory must actually exist.
- Write the `description`. It is what the session summary shows.
- Directories both products use belong under **both** `sources` lists. Leaving
  one out makes that registry report its own shared-code anchors as broken.

## Working in a monorepo

**Before writing a record**, read `.facetrc.yaml` and put it under the right
registry's records directory. A record written to the default `docs/facets` in
such a project is loaded by nobody.

**Ids are global.** The same id in two registries is the *same feature*,
documented independently by each product — one record each, own intent, own
contract, own lifecycle. That is what makes the products comparable.

Two consequences for you:

- If a feature exists in two products, give it **the same id in both**. Never
  invent `admin-auth/login/password` to avoid an overlap — that puts the
  registry name inside the id and throws the comparison away.
- Never cross-reference the two records in `lineage`. The shared id already says
  they are the same feature; a dependency edge would claim something else.

Two records for one id is the design, not duplication to clean up.

**Anchors in shared code** resolve against any registry that lists the
directory. Qualify only when the bare id exists in more than one:

```go
// @facet: storefront:auth/login/password
// @facet-role: related
```

A prefix naming a registry that is not declared is an error — every registry
skips such a tag, so the file silently leaves the registry entirely.

**Shared code proves no presence.** A record `active` in a registry needs an
anchor in code that registry alone claims. Qualifying a shared anchor settles
*which* registry it belongs to; it does not make shared code evidence that the
product reaches the feature. Same rule as variants, one level up.

## Reading the findings

| Finding | What it means |
|---|---|
| `config: registry "x" points at "…", which does not exist` | typo in `records`, or the directory was never created |
| `registry-refs: tag names registry "x", which is not declared` | bad prefix; the tag currently counts for nothing |
| `orphan-ids` … `another registry has this feature` | code exclusive to this registry implements a feature it has no record for — write one here too, or move the code somewhere both share |
| `orphan-records` … `anchored only in code shared with another registry` | warning: the feature may not be reachable in this product. Anchor its own code, or set `planned` |
| `[a] …` and `[b] …` on the same file | shared code, reported by each registry separately |

`facet lint --registry <name>` narrows a run to one registry while fixing it.
The commit gate always covers all of them.

## Comparing registries

`facet registries` prints the id × registry matrix — which product has which
feature, and at what lifecycle. `--gaps` shows only the ids some registry is
missing, `--json` for machine use. This is the payoff of global ids; reach for
it when the user asks what one product has and another does not.

If a row puts two clearly unrelated features side by side, the id was reused by
mistake. Rename one — the linter deliberately does not guess at this, because
the only test would be comparing two prose summaries against an arbitrary
threshold.

## Without the binary

The POSIX shell fallback reads only the flat top-level keys, so in a monorepo it
says it could not read the layout and that the commit gate did not run. That is
deliberate honesty, not a bug — but it means the binary should be available in
any repository that declares registries.
