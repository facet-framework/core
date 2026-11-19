# FACET Registries Guide

How to configure a project's FACET layout, and how to model a monorepo that
holds more than one product.

Spec reference: [§5.4 Project Configuration](framework/FACET.md#54-project-configuration).
Related: [Variants Guide](VARIANTS-GUIDE.md) — read that one first if your
repository holds two implementations of the *same* product.

---

## The short version

Most projects need one line, or none at all:

```yaml
# .facetrc.yaml
records: docs/facets
```

Everything below that is for repositories holding several unrelated products.

## A registry over independent source clones

One registry can document code in separate Git clones below its workspace:

```yaml
records: docs/facets
sources: [platform/backend, customers/x]
```

Declare these directories explicitly, even when the workspace's `.gitignore`
excludes them. Without `sources`, the scanner uses the outer repository's file
list and cannot see anchors in ignored clones. With `sources`, scan and lint
read those clones using their own ignore rules. The source entry must be inside
the clone; naming only its parent does not discover repositories recursively.

A declared source that is a **submodule** is treated the same way. The outer
repository records a commit inside a submodule as one changed gitlink, a path
with no anchors in it, so comparing only the outer repository would report
nothing for the files that actually changed. `facet drift` therefore compares
a declared submodule in its own repository, with its own review baseline, and
`--repository <submodule>` selects it like any other clone. A submodule that
is not declared in `sources` stays the outer repository's gitlink.

A registry that declares `sources` answers only for those paths. When the
workspace has anchored code of its own besides the clones, declare it too —
`sources: [., platform/backend]` — or its anchors are not this registry's.

`facet drift` compares the workspace repository and each declared source
repository against its own accepted baseline, falling back to its own HEAD.
The JSON `repositories` entries name each comparison and its status; finding
paths remain relative to the workspace. A HEAD-only entry does not establish
that incoming commits were reviewed, even when another clone has an acceptance.

For a baseline SHA belonging to one clone:

```sh
facet drift --repository platform/backend --baseline <reviewed-sha> --json
facet drift accept --repository platform/backend --outcome record-updated platform/backend/src/pay.go
facet drift reset --repository platform/backend
```

Review paths stay workspace-relative even with `--repository`. Without paths,
acceptance covers all changes in the selected repository; without a repository
selection, all declared source repositories are accepted. Updates run per
repository: an error in a later repository does not undo an earlier acceptance.
For a first partial review after a pull without an existing accepted baseline,
pass the confirmed previous revision to acceptance as `--baseline <reviewed-sha>`.
Otherwise the first acceptance starts at current HEAD, so incoming committed
changes outside the named paths cannot remain unreviewed. Later partial reviews
normally build on the accepted snapshot rather than reusing that older revision.

Reset removes the selected repositories' local review refs and returns them to
HEAD. Neither command changes project branches or records.

After a pull, merge, rebase or branch switch, use `/facet:catch-up` to inspect
the review window, reconcile affected records and accept only the reviewed
scope. For private anchors, `facet filter status` distinguishes missing blocks
originating in this worktree, another worktree, or an older sidecar with unknown
origin. Keep shared sidecars; a block absent here may still be needed elsewhere.

---

## Project layout and overrides

.facetrc.yaml commits the record, schema and source layout alongside the code
so local tooling and CI read the same configuration. FACET_RECORDS_DIR,
FACET_SCHEMA_DIR and FACET_SOURCE_DIRS remain supported and take precedence
for invocations such as CI jobs or migrations using a scratch tree. Put
persistent layout choices in the file so they can be reviewed.

---

## The decision that comes first

Before adding a second registry, be sure the answer is not **variants**.

| You have | Model it as |
|---|---|
| A web app and a native app | **Variants** — one registry |
| A legacy system and its rewrite | **Variants** — one registry |
| An EU deployment and a US deployment | **Variants** — one registry |
| A storefront and an internal admin tool | **Registries** |
| A SaaS product and its marketing site | **Registries** |
| A product and the CLI that automates it | **Registries**, usually |

The test is one question: **"does this feature exist over there yet?"**

If that question is meaningful — if someone would ask it, if a migration
dashboard would answer it — you have one product in several realisations, and
the answer is variants (§2.4). One record per feature, a lifecycle per variant,
and coverage is computable.

If the question is meaningless — nobody asks whether the admin tool has the
storefront's coupon checkout, because the admin tool has no checkout — you have
two products, and the answer is two registries.

Getting this wrong in the direction of registries still costs you: two
registries for a rewrite duplicates every intent and contract, and two normative
statements of one behaviour have no tie-break when they diverge — the exact
failure FACET exists to prevent.

But it is no longer catastrophic. Because ids are global (below), a split
rewrite keeps its coverage question answerable — `facet registries` still shows
which features v2 has and legacy has not. You pay with duplicated prose, not
with a lost answer.

Getting it wrong in the other direction is cheaper and self-announcing: a single
registry for two unrelated products fills one tree with features that have
nothing to do with each other, and everyone notices within a week.

---

## Setting up several registries

```yaml
# .facetrc.yaml
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

Rules the linter enforces:

- **Names follow the id grammar** (`^[a-z][a-z0-9-]*$`). A name is written in
  front of an id, so anything the anchor syntax cannot express is rejected.
- **`registries` is exclusive with a top-level `records`/`sources`/`schema`.**
  Both together leaves "which one does the linter mean" with no good answer.
- **A `records` path must exist.** A registry pointing at nothing has no records
  to find fault with, so it would pass every other check in silence.

`description` is optional and worth writing: it is what the session-start hook
shows next to each registry, so an agent reads "the internal back office"
instead of guessing from a directory name.

---

## Ids are global

`auth/login/password` in `storefront` and `auth/login/password` in `admin` are
**the same feature**, documented independently by each product. Two records —
own intent, own contract, own lifecycle, own Knowledge — joined by one id.

That is what makes the products comparable. "How does sign-in look in the
storefront, and in the admin tool" has an answer, and so does "what does the
admin tool not have yet":

```
$ facet registries
FEATURE                   admin    storefront
auth/login/password       planned  active
billing/checkout/coupons  —        active
ops/audit/trail           planned  —
```

**Choosing the same id is a claim, not an accident.** If two products mean
genuinely different things, pick different ids — that is how you say so. The
linter deliberately does not try to catch a mistaken reuse: the only way to
detect it would be to compare two prose summaries against an arbitrary
threshold, and a warning that fires wrongly is a warning that stops being read.
The matrix above surfaces it anyway, because a bad row puts two unrelated
features side by side where anyone can see them.

Where you want one product's answer rather than all of them, qualify the id:

```
storefront:auth/login/password
```

`feature_get` takes both forms. Bare, it returns every registry's record — that
is the comparison. Qualified, it returns one.

### What a registry actually separates

Not the namespace. The **specification**:

| | Variants (§2.4) | Registries |
|---|---|---|
| Records per feature | one | one per registry |
| Intent, contract | shared | independent |
| Comparable | within the record | across records, joined by the id |
| Use when | specified once, realised several times | each product specifies it for itself |

So the two are a spectrum, not a fork in the road, and the id runs through both.

---

## Shared code

Two registries may list the same directory:

```yaml
    sources: [apps/storefront, packages/shared]
    sources: [apps/admin, packages/shared]
```

`packages/shared` is then scanned by both. An `@facet:` tag in it resolves as
long as **either** registry holds a record for that id — a shared helper
implementing a storefront feature is not broken just because the admin registry
has never heard of it.

Four rules govern it:

**An id nobody owns is still an error.** The rule above is not an amnesty for
typos; it only means "some entitled registry has it", not "somebody probably
has it".

**Exclusive code gets no such benefit.** A tag in `apps/admin/` — a directory
only the admin registry claims — must resolve in the *admin* registry. Code
belonging to one product alone, implementing a feature that product has no
record for, is a defect however many other registries hold the id.

**Shared code proves no presence.** A record `active` in a registry needs an
anchor in code that registry alone claims. This is §2.4's "shared code proves no
variant", one level up — and with global ids it matters more, because a single
anchor in a shared helper would otherwise satisfy every registry documenting
that id at once. It is a warning, not an error: reachability is a question, not
a broken registry.

**Qualify the tag to say which product a shared file implements for:**

```go
// @facet: storefront:auth/login/password
// @facet-role: related
```

Note what this does and does not do: it settles *which registry* the anchor
belongs to. It does not make shared code prove presence — that still needs code
exclusive to the registry.

**A prefix nothing declares is an error.** Every registry skips such a tag as
somebody else's business, so an undeclared prefix makes the file invisible to
the whole registry — silently, which is worse than the typo. The linter reports
it once, naming the bad prefix.

---

## What the tooling does with all this

| | Single registry | Several registries |
|---|---|---|
| `facet lint` | as before | one pass per registry; every finding names its registry |
| `facet lint --registry admin` | n/a | reports only that one; the other registries stay loaded, so a `depends_on` or a shared anchor into them still resolves |
| Session start | one summary | one block per registry, with its description and the directories it owns |
| Commit gate | the project | **the whole project** — a commit is never gated on where the agent happened to be |
| `feature_get` | one-entry list | one entry **per registry** documenting the id; `registry:id` narrows it |
| `feature_find` | query | `registry:query` scopes the search |
| `anchor_list` | anchors | anchors labelled with the registry they sit in |
| `facet registries` | says there is nothing to compare | the id × registry coverage matrix; `--gaps` for the holes |
| Language server | ids from the registry | hover shows every registry's record side by side; go-to-definition offers each |
| `facet conventions` | the one conventions document, if any | each registry's effective document: its own `conventions:` override, else the shared one (see AUTHORING.md, "Project conventions") |

---

## Pinning the Core version

`requires.core` pins the exact FACET Core version the project expects. It is a
top-level key: a project with several registries still runs one binary, so the
pin applies to all of them and is never declared inside a `registries` entry.

```yaml
# .facetrc.yaml
requires:
  core: "0.1.0"
registries:
  shop:
    records: products/shop/docs/facets
```

Exact equality, no ranges; a development build `0.1.0-dev.…` does not satisfy
`0.1.0`. A mismatch stops every command except `version`, `help`, completion,
`setup …` and the git filters `filter clean|smudge` before
it does any work, exits non-zero, and names the configuration file, both
versions, how the binary was installed and the remedy that matches that
installation. The shell fallback below does not read the pin.

---

## The shell fallback cannot read a registries block

The plugin's POSIX shell hooks — the ones that run when no `facet` binary is
available — read only the flat top-level keys of `.facetrc.yaml`. Parsing a
nested registries block would need `yq`, and depending on a YAML toolchain is
exactly what the fallback exists to avoid.

So in a monorepo without the binary:

- session start says it could not read the layout, instead of reporting the
  default one, which in such a project is nobody's layout;
- the commit gate says it did **not** check, instead of exiting silently — a
  silent gate is indistinguishable from a clean registry.

Both are honest degradations, not failures. Make the binary available and they
go away.

The repository's own shell lint suite (`tools/facet-lint/run-all.sh`) is the one
exception: it reads the `records:` value of every entry in the block — a single
indented key, no YAML toolchain needed — and validates each records directory.
It does not read a registry's `sources:`; anchors are scanned from the whole
tree, minus what `.facetignore` excludes.

---

## For agents working in these repositories

1. **Read `.facetrc.yaml` before writing a record.** It tells you which records
   directory the record belongs in. Writing it to the default `docs/facets` in a
   monorepo puts it somewhere no registry will ever load.

2. **A record belongs to exactly one registry — but an id may span several.**
   If the same feature exists in two products, give it the same id in both, with
   a record each. Do not invent a second id to avoid the overlap, and do not
   cross-reference the two in `lineage`: the shared id already says they are the
   same feature.

3. **Never invent a registry.** The set is what `.facetrc.yaml` declares. If the
   feature you are documenting fits none of them, that is a question for the
   owner, not a new entry to add.

4. **Qualify anchors only where you must** — in shared code whose id exists in
   more than one registry. Everywhere else the bare id is correct, and a prefix
   is one more name to keep in sync.

5. **When asked to add a second registry, check it is not variants.** Ask the
   question in "The decision that comes first". If the answer is "yes, someone
   would ask whether the feature exists over there", say so and propose variants
   instead.

---

## Common mistakes

**Adding a registry for the rewrite.** The most likely wrong turn, and the one
that costs most. See the decision table.

**Leaving `sources` off in a monorepo.** A registry with no `sources` claims the
whole repository, so both registries scan everything and every anchor is
resolvable by both. Not an error, but the separation you set up stops meaning
anything.

**Listing `packages/` under one registry only, out of tidiness.** The other
registry then reports its own shared-code anchors as unresolvable. If both
products use it, both should list it.

**Putting the environment overrides in a shell profile.** They win over the
file, permanently and invisibly, for one person. That is the situation
`.facetrc.yaml` was added to end.

**Renaming a registry.** The name appears in every qualified anchor. Rename the
prefixes in the same change — the linter will report each stale one, but only
after the tag has already stopped counting.

**Inventing a second id to avoid an "overlap".** `auth/login/password` and
`admin-auth/login/password` are not two features, they are one feature and one
workaround. The shared id is the whole mechanism; a suffixed variant of it
throws the comparison away and puts the registry name inside the id, where it
does not belong.

**Reading a shared row in the matrix as duplication to clean up.** Two records
for one id is the design, not debt. They are one feature specified twice because
two products specify it for themselves.
