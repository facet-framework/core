# FACET Authoring Guide

Practical guidance for creating and maintaining FACET records. Adapted from FACET spec §10.

---

## Principles

- **Agents draft, humans own.** Tooling may generate first-draft content for any section, but every record must be reviewed and accepted by a human before it is considered authoritative.
- **Granularity is decided by the sizing test.** One record per named, deployable feature, and the five gates below say which candidates are one. The cases the gates genuinely leave open — and there are some — are the owner's.
- **Review is proportional.** Derived sections (Anchor, Lineage graph) may merge after CI passes. Prose and contract sections always require a human reviewer.

---

## Project conventions

A project may write its own authoring choices down once, so every author, agent and reviewer reads the same text: the working language of prose, terms that are never translated, what an intent summary is for, where unresolved claims go. It lives in one Markdown document (spec §5.4.4):

| Precedence | Where | Source label |
|---|---|---|
| 1 | `conventions:` under a registry in `.facetrc.yaml` | `registry` |
| 2 | top-level `conventions:` in `.facetrc.yaml` | `project` |
| 3 | `FACET-CONVENTIONS.md` at the repository root | `default` |

The first that applies wins, and documents are never merged. In a monorepo, every registry shares the project's document unless it declares its own on purpose. `facet conventions` prints the effective document per registry. The session hooks name the same path, so authoring, adoption and review all read the same one.

```yaml
# .facetrc.yaml
conventions: docs/facets/CONVENTIONS.md
registries:
  storefront:
    records: apps/storefront/docs/facets
    conventions: apps/storefront/CONVENTIONS.md   # deliberate override
  admin:
    records: apps/admin/docs/facets               # uses the shared document
```

What belongs in it:

- **Prose language and protected identifiers.** For example: prose in German, while schema keys, ids, code identifiers and the Gherkin keywords `Given`/`When`/`Then`/`And` stay as they are. Say what that costs: readers of another language need a translation.
- **What an intent summary is for.** A few sentences about capability and purpose, not a compressed inventory of the record.
- **Readable engineering statements and independent Gherkin steps.**
- **Metadata use.** Short provenance and annotation, not a second contract or an investigation transcript.
- **Where unresolved claims go.** A review task, or an existing `assumption`, `deviation` or `backing` entry, rather than prose in the record.
- **Decisions, with the alternatives and their drawbacks.** Link an ADR where one exists rather than repeating it.

What it is not:

- **Not configuration.** No tool reads its contents. A rule that should be enforced belongs in validated configuration, not in prose.
- **Not above the schema or the spec.** Where it contradicts either, they win. Change the document, not the record.
- **Not a permission.** It cannot allow a push, an approval or a merge, and it does not replace the human review the principles above require.

A project with none of the three has no conventions document, and nothing changes.
Sibling records establish neither this feature's facts nor project conventions.
Use simple, concrete prose throughout the record, without metaphor or
personification. Prefer YAML mappings and literal `statement: |` blocks for
readable Given/When/Then steps; use JSON-style or escaped statements only when
the effective conventions explicitly require them.

---

## When to create a record

- **New feature PR** → create the record in the same PR as the implementation.
- **Refactor with no behaviour change** → no new record; update Lineage/Verification on the affected record if anchors move.
- **Back-fill for shipped features** → only during a dedicated FACET adoption sprint with the same review gates applied.
- **Unsure?** Draft only supported facts. A valid planned stub still requires `schema_version`, `id`, `intent.summary`, `contract.scenarios` (which may be empty), `lineage.owner.team` and `lineage.created_at`, plus exactly one of `lifecycle` or `variants`. An unknown owner remains unresolved and blocks validation; do not invent a team to complete the draft.

---

## Is this one feature? The sizing test

Decide this before you open a file. It is the decision with the widest spread
between people and between sessions, and the one a record cannot be repaired
from later: an id is immutable for the life of the product.

All five gates must hold. Four out of five almost always means you are one id
segment away from correct — go up a level, or go down one.

| | Gate | Fails when |
| --- | --- | --- |
| 1 | **Name** — nameable as `<verb> the <object>`, or a noun phrase a user would say unprompted | it contains "and" (that is the split line), or names a technology, a class, or a layer |
| 2 | **Sitting** — one user, one sitting, one outcome they would call done | it spans sessions, or it is a step nobody would ask for alone |
| 3 | **Contract** — two to nine observable scenarios, including a failure path | fewer than two: it is a scenario of its parent. More than nine: the scenarios have formed groups that never reference each other, and those groups are the features |
| 4 | **Removal** — one sentence says what a user loses, and the product still stands | no sentence: it is implementation. The product falls over: it is not separable |
| 5 | **Owner** — one team could own it and would recognise the name on a roadmap | two teams must both agree: the boundary crosses a seam |

**When two readings both pass, take the coarser one — the record with more
scenarios.** Not a style preference. A scenario costs an id inside a record. A
record costs an immutable id, an anchor to maintain, an owner, five Knowledge
slots that warn while empty, and a node in the dependency graph. Splitting later
is mechanical; merging later costs two dead ids and a redirect that outlives
everyone who remembers why.

### What is not a feature

Each of these already has a home. Giving it a record is how a registry fills up
with entries nobody maintains:

| Candidate | Where it belongs |
| --- | --- |
| A service, controller, store, hook, module, util | an `@facet:` anchor on a record |
| One CRUD verb of a set with no distinct rules | one scenario of the feature that owns the object |
| A screen, page, or route | `knowledge.discoverability` |
| An API endpoint | implementation — unless the API is the product surface |
| A feature flag or config switch | `flags[]` |
| A bug fix | `defects.fixes[]` |
| A rewrite, or a second client of the same feature | a variant — never a second record |
| Something serving one other feature, with no name of its own | a scenario there, or `lineage.depends_on` |
| "Fast", "accessible", "compliant" | `observation` SLOs, or a policy record |

### Worked example

Offered as three features: "create playlist", "rename playlist", "edit playlist".

Each fails gate 3 alone — create has perhaps two scenarios of its own — and each
fails gate 4, since a product with create but no rename is not one anybody
shipped deliberately. They are **one** record with five scenarios: create,
rename, reorder, delete, restore.

"Playlist cover art" *is* separate: its own rules (image validation, moderation,
a generated fallback), its own failure paths, a plausible separate owner. All
five gates hold.

"Invite a collaborator" and "remove a collaborator" are symmetrically one
record — collaboration — unless the invite carries an acceptance flow and a
notification contract heavy enough to pass gate 3 by itself, which in a real
product it usually does. The gates make that arguable rather than arbitrary,
which is the whole objective.

`facet lint` has a backstop for gate 3 (check `sizing`), and it sits wider than
the gate on purpose — it warns at one scenario and above twelve, not at ten,
because the gate is what you aim at and the linter is what catches a record
nobody aimed. `facet calibrate` reports whether the registry as a whole has
drifted to one side. The other four gates are yours.

---

## Step-by-step: creating a new record

### 1. Determine the ID

Three lowercase kebab-case segments: `domain/capability/feature`.

```
payments/checkout/stripe-session
auth/webauthn/credential-registration
notifications/email/delivery-status
```

Rules:
- `domain` is a **product** domain — an area of the product a user or an operator
  would name (`payments`, `catalog`, `identity`). **Not a team and not a
  service.** Teams reorganise and ids never change, so an id built from the org
  chart is wrong on a schedule; the team goes in `lineage.owner.team`, which
  exists so the two stay apart.
- `capability` is a stable noun group within that domain
- `feature` names the specific named capability

If the project declares a taxonomy (`docs/facets/taxonomy.yaml`), the first two
segments must resolve against it and `facet lint` fails otherwise. Read it before
choosing an id — and if your feature fits none of the declared capabilities, run
the sizing test again before adding one. A feature that fits nowhere is usually a
feature sized wrong. See [`TAXONOMY-GUIDE.md`](TAXONOMY-GUIDE.md).

### 2. Stub the file

Create `docs/facets/features/<domain>/<capability>/<feature>.yaml`:

```yaml
schema_version: "0.1"
id: <domain>/<capability>/<feature>
lifecycle: planned
intent:
  summary: |
    At most three sentences: what this feature does and why it exists.
contract:
  scenarios: []
knowledge:
  prerequisite_knowledge:
    question: "What must the user already understand to use this feature?"
  not_for:
    question: "When is this the wrong tool for the job?"
  discoverability:
    question: "Where does the user enter this flow in the product?"
    derive_from: [anchors]
  common_pitfalls:
    question: "What mistakes do users typically make?"
  success_signal:
    question: "How does the user know it worked?"
lineage:
  owner:
    team: <your-team>
  created_at: "<YYYY-MM-DD>"
  superseded_by: null
  depends_on: []
  depended_on_by: []
```

The `knowledge` block is **optional** — empty slots produce a linter warning
(never an error). Draft all five answers that the sources support; leave a genuine
unknown answer absent and identify the missing owner input in the handover.
Question text comes from the deployment-wide template; only `answer` is
record-specific. `derive_from` tells the generator which sources to load
(FACET §8.5); it is not an answer. For `discoverability`, provide the real
UI path, CLI command or public API entry when evidence establishes it.

### 3. Write the Intent

`intent.summary` should answer two questions:
- **What** does this feature do (observable behaviour)?
- **Why** does it exist (business/product motivation)?

Write **one or two short sentences** naming the general capability and purpose.
Specific formats, versions, limits, branches and failure details belong in
scenarios. Put user guidance in Knowledge and code rationale beside the anchor.
If the capability still needs a long summary, check its granularity with the
sizing test.

Link to ADRs or design docs via `intent.decision_records` rather than
retelling them here.

### 4. Write the Knowledge slots

Knowledge captures what a _user_ needs to know, not what the system does. Five slots, all optional, all phrased as questions:

| Slot                      | Owner-supplied?      | Generator can derive?     |
| ------------------------- | -------------------- | ------------------------- |
| `prerequisite_knowledge`  | Yes                  | No                        |
| `not_for`                 | Yes                  | No                        |
| `discoverability`         | Optional             | Yes (from anchors)        |
| `common_pitfalls`         | Yes                  | Partially (failure tests) |
| `success_signal`          | Yes                  | No                        |

Rules of thumb:

- **Keep the invariant question and draft a grounded answer.** Read setup, permissions, limits, actual entry points, failure cases and visible confirmation. Do not treat generator routing as a completed answer or substitute an internal assignment for the user's success signal. Distinguish inferred pitfalls from observed usage history.
- **Keep answers short and preserve exact timing, scope and effects.** Separate state changes may take effect at different times. Do not conflate them or add a causal explanation the sources do not establish.
- **Resolve source conflicts explicitly.** Scope answers to actual observable behavior for the stated lifecycle and interface. If an owner statement conflicts with implementation evidence, answer the known behavior and flag the unresolved conflict briefly outside the YAML; do not present a desired promise as implemented.
- **Empty is better than `tbd`.** The linter warns on placeholder strings (`tbd`, `todo`, `xxx`); they pollute generated artifacts.
- **Be specific.** "Which permissions are required to apply a discount when one is already on the cart?" beats "Permissions needed?"
- **`not_applicable` is the owner's, and only the owner's.** A slot may declare that its question has no answer for this feature (FACET §3.6.1), with a `reason` and a `confirmed_by`. It is a decision, not a cleanup: an agent that must not invent an answer must not invent an exception either, and it is the cheaper of the two to write. Leave the slot empty and let it warn; clearing it is the owner's call.
- **Internal is not the same as inapplicable.** A record with all four owner slots empty often has a sibling in its own capability that answered those same four — CI-only checks among them, with substantial `common_pitfalls` — so an internal feature is rarely one whose questions have no answer. Expect the count to stay well above zero after adopting the field. A registry reporting no knowledge gaps has most likely mislabelled its Knowledge facet rather than completed it.

### 5. Write the Contract

A scenario may use `specified_by: <source>:<spec-id>#<case>` to refer to a
pinned specification scenario. Keep the Gherkin steps and any example tables
in the record. Lint compares them offline against the pin. Use
`lineage.specified_by` for file-level provenance. See FACET §3.2.5.

Add at least two scenarios: the happy path and one failure/edge case.

```yaml
contract:
  scenarios:
    - id: session-created
      statement: |
        Given a user selects a paid plan
        And the plan has a trial period
        When they click "Subscribe"
        Then a Checkout session is created
        And the user is redirected to payment
      post_state:
        session_status: open
    - id: invalid-plan-rejected
      statement: |
        Given an unrecognised plan ID is passed
        When CreateCheckoutSession is called
        Then an error is returned
        And no session is created
      post_state:
        session_created: false
```

**One step per line, and `And` for the next one.** Begin each step with the exact
`Given`, `When`, `Then` or `And` keyword followed by a space, without intervening
punctuation or conditional prose. Bind varying preconditions to concrete example
columns or separate cases. Every condition, action and consequence gets a line
of its own. Two of anything — two Givens, two
Whens, two Thens — is a second line beginning with `And`, never a longer
sentence:

```
Then a Checkout session is created
And the user is redirected to payment
```

and not:

```
Then a Checkout session is created and the user is redirected to payment
```

Why it is worth the extra line: a `Then` holding two consequences in one sentence
is a promise that cannot be disproved one half at a time. A test that satisfies
one half and misses the other cannot be pointed at such a scenario honestly, and
a `verification` entry naming it then claims more than anyone checked. Separate
lines are separately provable. It is gate 1 of the sizing test one level down —
what cannot be named on its own cannot be held on its own.

**An "and" inside a single step is not this.** `Given a record with a code and a
name` is one condition about one object that happens to have two fields, and it
stays one line. The rule is about independent statements that were joined
together, not about the word:

```
Given a record with a code and a name      # one condition — leave it
Given a user selects a paid plan
And the plan has a trial period            # two conditions — two lines
```

Nothing checks this. Telling a joined pair from a single condition means reading
the sentence, and a check over the word "and" would fire on the first line above
and stay silent on a `Then` whose halves are joined by a comma. It is on you and
your reviewer. See FACET §3.2.

Write the contract in the language of the actual product surface. Unit assertions
are evidence for behavior, not a requirement to describe internal assignments
or exceptions. For example, an assertion that `access_until == period_end`
supports "access continues until the paid period ends". An internal
`PermissionError` may support rejection without subscription changes; it does
not prove the product exposes that exception. Check callers and entry points.
Exact exceptions, statuses and exit codes belong in statements when that
interface is the product and the evidence establishes them. Useful internal
precision can sit in `pre_state` or `post_state`. Do not turn every internal
branch into a promised product flow: a parser exception or missing key can be
an incidental failure rather than supported behavior. Use `deviation` for
confirmed unwanted behavior where applicable, or flag an uncertain contract in
the handover. An exception alone does not establish "no changes"; check mutation
order and assertions before promising preservation.
Engineering contracts may describe supported operational behavior, including
telemetry. State the observable guarantee rather than merely a function call or
code branch.

Scenario IDs must be stable kebab-case — they are referenced by tests in `verification`.

If one rule holds for **several cases that differ only in their values**, write it
once with parameters and put the cases in a table rather than flattening them
into the sentence. `Status ∈ {Open, Confirmed}`, "applies to A, B and C" and "an
admin or an owner" are all test matrices written where nothing can read them:

```yaml
    - id: an-id-is-three-lowercase-segments
      statement: |
        Given a record id <id>
        When the linter checks it
        Then it is <verdict>
      examples:
        - name: valid ids
          columns: [id, verdict]
          rows:
            - ["billing/invoice/list", "accepted"]
            - ["registry/integrity/record-validation", "accepted"]
        - name: invalid ids
          rows:
            - ["Billing/Invoice", "rejected"]
            - ["billing/invoice/list/extra", "rejected"]
```

Every example row must satisfy all of its scenario's `Given` preconditions.
Example cells substituted into literal commands must contain actual argument
values, not explanations; an empty cell may represent an omitted option.
Put explanations outside such cells.
State permissions, flag states and boundaries that distinguish success from
failure; compare related scenarios so identical conditions do not promise
contradictory results.
Every `pre_state` and `post_state` claim must hold for every represented case.
Qualify case-specific claims explicitly or use separate scenarios.

Four things to know:

- **One rule per block, and name the blocks.** Valid cases and invalid cases are
  two rules. Put them in one table and you have written one rule with a result
  column, which is the habit the field replaces — moved into a grid.
- **Columns are inherited.** A block that declares none uses the columns of the
  first block that does. At least one block has to declare them. Blocks may also
  differ; each table draws its own headings.
- **The key is `columns`, not `cols`**, and a block rejects any key it does not
  know — a block whose headings were quietly ignored would render its values
  under the ones it inherited.
- **The table counts as one scenario**, not one per row, everywhere a scenario is
  counted. That is the point: consolidating cases never costs what writing them
  as separate ids would.

`facet lint` checks the two things the schema cannot: a row whose length
disagrees with its columns is an error, and a `<placeholder>` no column answers
is a warning — it is never substituted, so the table under it fills nothing. A
column no placeholder uses is fine and nothing says otherwise; a `why` or `notes`
column is ordinary Gherkin. `And` steps need none of this — `statement` is prose
and always was. See FACET §3.2.3.

If a scenario describes behaviour whose **data does not exist yet** — the code
ships, the surface renders its empty state, and the store behind it has not been
built — declare that on the scenario rather than in prose:

```yaml
    - id: spend-by-category-shown
      statement: |
        Given a customer with categorised transactions
        When they open the spending breakdown
        Then spend is grouped by category
      backing:
        state: unbacked
        reason: transactions carry no category column yet
        unblocked_by: ledger/transactions/categorisation
```

`reason` and `unblocked_by` are both required when `state: unbacked` — the record
will not validate without them, because an unexplained gap is indistinguishable
from an oversight. Write `unblocked_by` as a feature id where one exists: it then
becomes a dependency edge, and the linter tells you when that feature goes active
while the gap is still declared. A store name or an issue reference is fine too;
it is simply not resolved.

An absent `backing` means backed, so nothing changes for scenarios that are.
Declaring a gap is not a defect and nothing warns about it. `facet backing` lists
every gap currently declared. There is no `partial` lifecycle — a feature whose
empty state ships is `active`. See FACET §3.2.1.

If a scenario describes behaviour the code **actually has and nobody wants** — a
tenant filter built and never applied, an expiry parsed and discarded — say so
on the scenario rather than writing the bug up as a contract or leaving it out:

```yaml
    - id: dataset-delete-ignores-tenant-scope
      statement: |
        Given deleteDataSet is called with another tenant's id
        When the query runs, with no tenant predicate applied
        Then the row is found regardless of tenant
        And it is deleted
      deviation:
        reason: |
          A $scopeFilter is built and never applied to the query. A sibling
          method in the same controller checks the tenant directly.
        confirmed_by: code-review
        tracked_as: null
```

Three rules matter more than the syntax:

- **Name the id after what the code does, never after what it should do.**
  `dataset-delete-ignores-tenant-scope`, not `dataset-delete-is-tenant-scoped`.
  The id is what a diff, a search and a summary show without opening the record,
  so an id that claims the fix travels further than the record that denies it.
  Nothing checks this — it cannot be checked without knowing the intended
  behaviour, which the record deliberately does not state. It is on you and your
  reviewer.
- **Do not write down what the behaviour ought to be.** There is no field for it
  and the schema rejects one. A suspected intent sitting next to an observed one
  is indistinguishable from a contract. When a fix is decided, it goes in a
  ticket or a separate `planned` record, named by `tracked_as`.
- **`reason` is required**, and `deviation` cannot sit beside
  `backing: unbacked` — that combination says the gap is intended and the
  behaviour is not.

`confirmed_by` is `code-review`, `test` or `runtime`, and saying nothing means
nobody recorded how it was established — not that it is unconfirmed. Write
`tracked_as` as a feature id where the fix has a `planned` record: the linter
then tells you when that record goes active and the deviation was left behind.
`facet deviations` lists every one currently declared. See FACET §3.2.2.

### Check evidence across the record

Read the current schema and its nested definitions. Review every section for
applicable facts in implementation, tests, configuration and documentation:
scenario states and variant scope, decision records, all five Knowledge slots,
verification, operational SLOs/alerts/checks (`observation`), actual telemetry
emissions (`observability`), flags, defect attribution, lineage relationships
and authorship. Populate useful supported facts; optional fields may remain
absent when no relevant evidence exists. Do not add empty decorative blocks or
invent facts to make the record look complete.

Read exact test assertions before adding `verification`. Account for every
existing test relevant to the feature's behavior, including operational and
telemetry assertions. Include the supported guarantees they establish and their
verification; explain any relevant test left unreferenced in the handover.
For file-based tests,
use `id: path/to/test_file::exact_selector`, including a class when required.
`proves` names only scenarios whose conditions and consequences are established
by those assertions, including the represented outline cases. Before setting
`proves`, establish `Given` conditions and `pre_state` through controlled test
setup or assertions. Find exact assertions, including their selectors, for every
`Then`, subsequent `And` result and `post_state` claim. Production code alone is
not test proof. A metric-value assertion alone does not prove its kind or labels;
specific type or label selectors or assertions can establish them.
Limit a tested operational scenario to asserted guarantees;
retain source-supported kinds and labels in structured `observability`. Narrow
or split partially proved scenarios when that reflects distinct behavior,
otherwise report the proof gap briefly outside the YAML rather than setting
`proves`. A similar test name, fixture or unasserted call is not proof.
Leave uncovered scenarios without
fabricated links and report the proof gap outside the YAML. Distinguish tests
read from tests run.
Before finishing, cross-check every asserted case or branch in each relevant
test against all applicable scenarios. Map every scenario it fully proves,
rather than stopping at the first case or variant. Recheck every outcome,
including supplemental `post_state`: remove unasserted extra invariants from
test-proved scenarios, or document useful source-grounded behavior separately
without claiming proof. Ensure example columns that vary conditions or actions
are bound to step placeholders; explanatory columns need not be bound.

### 6. Set lifecycle and add anchors

If the feature is not yet implemented:
- Keep `lifecycle: planned`
- No `@facet:` tags needed yet

When the implementation lands:
- Add `@facet: <id>` + `@facet-role: primary` to the implementing files (see [ANCHOR-GUIDE.md](ANCHOR-GUIDE.md))
- Change `lifecycle: planned` → `lifecycle: active`
- Both changes must be in the same PR

### 7. Validate locally

```bash
sh tools/facet-lint/run-all.sh
```

Schema validation requires `yq` (mikefarah v4) and `ajv-cli` (`npm install -g ajv-cli`). The orphan checks only need standard POSIX tools.

### 8. Open a PR

Title format: `feat(facet): add <id>` or `feat(<team>): add <id>`.

The CI `facet-lint` workflow runs automatically and will block merge on:
- Schema validation errors
- `@facet:` tags that reference unknown record IDs
- `active` records with no source anchor

---

## Features that exist more than once

If the repository declares variants — a web and a native client, a legacy system
and its rewrite — a record spanning them replaces `lifecycle` with a per-variant
map:

```yaml
variants:
  web:
    lifecycle: active
  ios:
    lifecycle: planned
```

`lifecycle` and `variants` are mutually exclusive; a record carries one or the
other. An absent variant means the feature does not apply there, which is not a
gap.

**Do not split a record because a feature has two implementations.** One feature
is one record. The test is the intent: if a single paragraph is honestly true of
both, they are one feature, and behaviour that genuinely differs is expressed as
a variant-narrowed contract scenario. Splitting duplicates the intent and the
contract, and the copies drift.

Full guidance, including when a variant is the wrong model: [`VARIANTS-GUIDE.md`](VARIANTS-GUIDE.md).

If the repository holds several unrelated products, it may declare a registry
each in `.facetrc.yaml` — check it before deciding where a record goes, and
remember that ids are unique per registry rather than per repository:
[`REGISTRIES-GUIDE.md`](REGISTRIES-GUIDE.md).

---

## Updating an existing record

| Change type | Record update required? |
|-------------|------------------------|
| New test that proves a scenario | Yes — add to `verification` |
| New `@facet:` anchor added/moved | Yes — update `lineage` if the code footprint changes materially |
| SLO or alert rule changed | Yes — update `observation` |
| User-visible behaviour change that invalidates a Knowledge answer (e.g. permissions changed → re-review `prerequisite_knowledge`) | Yes — update affected Knowledge slot |
| Internal refactor, no behaviour change | No |
| Dependency version bump | No |

---

## Deprecating a record

```yaml
lifecycle: deprecated
lineage:
  superseded_by: payments/checkout/stripe-session-v2
```

The record remains in the registry until the sunset date. After sunset, move it to `docs/facets/graveyard/`. IDs are never reused.

---

## Record sections reference

| Section | Required | Who writes it |
|---------|----------|--------------|
| `schema_version`, `id`, exactly one of `lifecycle` or `variants` | Yes | Author |
| `intent.summary` | Yes | Author (human-reviewed) |
| `contract.scenarios` | Yes | Author (human-reviewed) |
| `contract.scenarios[].examples` | No | Author (an agent may draft rows from cases it found, never rows it has not seen a reason for) |
| `contract.scenarios[].backing` | No | Author (owner-supplied `unblocked_by`) |
| `contract.scenarios[].deviation` | No | Whoever read or observed the code; never states the intended behaviour |
| `verification` | No | Author or agent |
| `observation` | No | Author or agent |
| `observability` | No | Author or agent (the implementing developer) |
| `knowledge.prerequisite_knowledge` | No | Author (owner-supplied; warning if empty on `active`) |
| `knowledge.not_for` | No | Author (owner-supplied) |
| `knowledge.discoverability` | No | Author or agent (derivable from anchors) |
| `knowledge.common_pitfalls` | No | Author (owner-supplied) |
| `knowledge.success_signal` | No | Author (owner-supplied) |
| `lineage.owner`, `created_at` | Yes | Author |
| `lineage.depends_on` etc. | No | Author or agent |
| `authorship` | No | Agent (injected) |
| `metadata` | No | Tooling (never normative content) |

### Test references and uninitialized submodules

`verification-targets` checks file-shaped test ids against the checkout.
When the test is missing inside a genuine, uninitialized Git submodule,
`facet lint` reports a warning naming the submodule path, with a
`git submodule update --init -- '<path>'` hint. After initializing, run the
lint again: only then can it check the referenced file. The warning does not
claim that a test exists or has run.

What counts is a gitlink in the Git index (mode `160000`) together with a
missing `.git` marker at the submodule path. Git itself reads its index with
NUL-separated paths; a `.gitmodules` declaration alone cannot hide missing
tests. In initialized submodules, with damaged existing Git metadata, and
outside the submodule boundary, a missing test remains an error. Without a
readable Git index the normal file check applies as well. The record schema
and the anchor rules do not change.
