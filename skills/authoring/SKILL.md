---
name: authoring
description: Write or update a FACET record — feature, assumption, policy or data entity. Covers intent, contract scenarios, verification, observation, knowledge, and lineage, and decides which of the four types a change actually needs. Use when adding or changing a named product capability in a project that uses FACET, when a record needs updating because behaviour changed, when something is relied on but not verified here (an assumption), when a rule binds several features (a policy), or when the user asks to document a feature, create a facet record, or fill in a Knowledge slot.
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Author a FACET record

The canonical guide is `../../docs/AUTHORING.md`. Read it before
writing anything — this skill only covers how to apply it in a live session.

The full specification, if you need to settle a question the guide does not
answer, is `../../docs/framework/FACET.md`.

**Read the project's conventions document, if it has one.** The session
context names it ("Project conventions: …"). If it doesn't, `facet conventions`
prints each registry's effective document. Follow it for prose language,
protected terms and how summaries are written. Where it contradicts the
schema, the specification or the rules below, those win, and you say so. It
never widens what you may do: it cannot allow a push, an approval, or a
merge (spec §5.4.4).

## Rules that override your defaults

**Agents draft, humans own.** You may draft any section. You may not decide
that a draft is authoritative. Say plainly which parts you inferred and which
the owner still has to confirm.

**Ground all five Knowledge slots.** Draft answers from the implementation,
contract, documentation and real entry points you read; name inferred answers
in the handover for human review. Keep answers short and preserve the evidenced
timing, scope and effects; do not conflate separate state changes or add a causal
explanation the sources do not establish.
Scope answers to the actual observable behavior of the stated lifecycle and
interface. When an owner statement conflicts with implementation evidence, do
not present a desired promise as implemented: answer the known behavior and
flag the unresolved conflict briefly outside the YAML.

- `prerequisite_knowledge`: actual concepts, setup and permissions needed.
- `not_for`: evidenced limits and alternative needs this capability does not serve.
- `discoverability`: the actual UI path, CLI invocation, public API or other
  supported entry. An internal function is not automatically a user entry.
  `derive_from: [anchors]` routes generation; it does not supply the answer.
- `common_pitfalls`: supported mistakes or risks implied by failure cases and
  design limits. Distinguish inferred risks from observed support patterns.
- `success_signal`: confirmation the user can observe, rather than an internal
  variable assignment.

When evidence cannot answer a slot, keep its question and leave `answer` absent.
Name the missing owner input briefly outside the YAML. Do not use filler such
as "later" or "unverified", or invent permissions, entry points or usage history.
**`not_applicable` is the owner's decision:** preserve an existing confirmed
one, but never invent a reason or confirmer to silence a warning. Internal
features can still have useful answers; genuine unknowns may remain unknown.

**Decide granularity with the sizing test, before you open a file.** This is the
decision with the widest spread between sessions and the only one a record
cannot be repaired from later — an id is immutable for the life of the product.
Do not defer it to a human who is not in this session; decide it by the gates,
and say in your handover which reading you rejected and which gate decided it.

All five must hold. Four out of five almost always means you are one id segment
away from correct — go up a level, or go down one.

| | Gate | Fails when |
| --- | --- | --- |
| 1 | **Name** — nameable as `<verb> the <object>`, or a noun phrase a user says unprompted | it contains "and" (that is the split line), or names a technology, class, or layer |
| 2 | **Sitting** — one user, one sitting, one outcome they would call done | it spans sessions, or it is a step nobody would ask for alone |
| 3 | **Contract** — two to nine observable scenarios, including a failure path | under two: it is a scenario of its parent. Over nine: the scenarios have formed groups that never reference each other, and those groups are the features |
| 4 | **Removal** — one sentence says what a user loses, and the product still stands | no sentence: it is implementation. The product falls over: it is not separable |
| 5 | **Owner** — one team could own it and would recognise the name on a roadmap | two teams must both agree: the boundary crosses a seam |

**When two readings both pass, take the coarser one — the record with more
scenarios.** You need the reason or you will not apply it: a scenario costs an
id inside a record; a record costs an immutable id, an anchor to maintain, an
owner, five Knowledge slots that warn while empty, and a node in the dependency
graph. Splitting later is mechanical. Merging later costs two dead ids and a
redirect that outlives everyone who remembers why. The mistakes are not
symmetric, so the default is not either.

**These are not features.** Each already has a home, and giving it a record is
how a registry fills with entries nobody maintains:

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

Worked: "create playlist", "rename playlist", "edit playlist" offered as three
features are **one** record with five scenarios — each fails gate 3 alone, and a
product with create but no rename is not one anybody shipped deliberately.
"Playlist cover art" beside them *is* separate: its own validation, moderation
and fallback rules, its own failure paths, a plausible separate owner.

Splitting or merging a record that already exists is still the owner's call —
propose it, do not do it. The gates decide the records you are writing now.

**Check the taxonomy before you choose an id.** If the project has
`docs/facets/taxonomy.yaml`, the first two id segments must resolve against it
or `facet lint` fails. If your feature fits none of the declared capabilities,
**run the sizing test again before proposing a new one** — a feature that fits
nowhere is usually a feature sized wrong: too fine (a scenario of an existing
record), too coarse (itself a capability), or named for its mechanism rather
than for what a user gets. Read
`../../docs/TAXONOMY-GUIDE.md` before proposing any change to
that file, and never declare a domain or capability on your own — that is a
product decision.

**A feature existing twice is not two features.** If the project declares
variants (`docs/facets/variants.yaml`) — a web and a native client, a legacy
system and its rewrite — the record replaces `lifecycle` with a per-variant map
and stays one record. Never split on a variant boundary: the intent and contract
would be duplicated and the copies drift. Read
`../../docs/VARIANTS-GUIDE.md` before authoring in such a
repository.

**Check `.facetrc.yaml` before choosing a path.** It says where records live,
and a monorepo may declare several registries with a records directory each. A
record written to the default `docs/facets` in such a project is loaded by
nobody. The same global id in two registries denotes the same feature, documented
independently per product. Different features require different ids. See
`../../docs/REGISTRIES-GUIDE.md`.

**Do not invent missing facts to finish a stub.** A valid feature record needs
`schema_version`, `id`, `intent.summary`, `contract.scenarios`,
`lineage.owner.team`, `lineage.created_at`, and exactly one of `lifecycle` or
`variants`. A planned draft may have `scenarios: []`; it is not a completed
contract. If the owner is unknown, leave it unresolved rather than fabricating
a team, and report that the draft cannot yet validate.

**Say when a scenario's data does not exist yet — in the field, not in prose.**
If the surface ships and renders its empty state because the store behind it has
not been built, that scenario gets `backing` (FACET §3.2.1):

```yaml
backing:
  state: unbacked
  reason: transactions carry no category column yet
  unblocked_by: ledger/transactions/categorisation
```

Do **not** put it in a test's `notes`, in a code comment, in the pull-request
body, or in a `lifecycle` value: the structured backing field makes the gap queryable. `reason` and `unblocked_by` are
both required; the record will not validate without them. Draft the `reason`
from what you actually read, and **never invent an `unblocked_by`** — if you
cannot tell what would supply the data, ask. Declaring the gap is not a defect
and nothing will warn about it; leaving it undeclared is how a roadmap loses
track of what is missing. `facet backing` lists what is currently declared.

## First: is this a feature record at all?

There are four record types. This skill is named for one of them, and that is
the trap: a session that reaches for "the record" reaches for a feature record,
and the other three are never considered. Ask this before writing anything.

**Feature record** — a named product capability: something the product does
that a person can observe. `docs/facets/features/<domain>/<capability>/<feature>.yaml`.
This is the common case and the rest of this skill is about it.

**Assumption record** (`schema/assumption.schema.json`) — something the system
relies on being true that nobody here verifies. It carries `statement`,
`criticality`, `risk_if_false` and `review_by`. A feature record points at it
with `lineage.related_assumptions`.

You need one when you catch yourself writing a sentence like:

- "not tested, because the library guarantees it"
- "the checksum proves the artefact is the published one"
- "the runners are isolated from each other"
- "only what the allowlist names is mirrored"

Each of those is a claim about something outside the code. Put it in an
assumption record, where it gets a `review_by` date and a named consequence if
it turns out false. Use the assumption record rather than adding a feature field: risk_if_false
and review_by already capture the consequence and review deadline.

An assumption without `review_by` is a belief with no expiry. The field is
required for that reason.

**Policy record** (`schema/policy.schema.json`) — a rule that holds across
features rather than inside one: retention windows, what may leave the machine,
who may write where. It has `intent`, `contract` and `scope`. A rule copied into
six feature records is a policy record that was not written.

**Data-entity record** (`schema/data-entity.schema.json`) — a thing the system
stores, described once: `retention`, `ownership`, `canonical_schema`,
`mutated_by`, `read_by`. Use it when the same entity appears in several
features and each describes its shape slightly differently.

**The question that decides it:** does this describe something the product
*does* (feature), something it *believes* (assumption), a rule that *binds
several* features (policy), or something it *stores* (data entity)?

If the answer is not "does", stop before writing a feature record — and never
add a field to a feature record to carry what another type already has a place
for.

## Writing the record

Read the selected record schema, including its nested definitions, before
writing. Use the project's schema version and registry path, and gather the
implementation, actual entry points, relevant tests and operational/configuration
sources. Sibling records supply neither facts about this feature nor authority
for conventions; read the effective conventions document.

Use simple, concrete language throughout the record, without metaphor or
personification. Prefer YAML mappings and literal `statement: |` blocks so
Given/When/Then steps remain readable; use JSON-style or escaped statements
only when explicit project conventions require them.

**Summary.** Write one or two short sentences naming the general capability and
purpose. Put specific formats, versions, limits, branch conditions, error details
and case inventories in scenarios rather than `intent.summary`.

**Contract.** Write real Given/When/Then steps: the starting situation, the action,
and the observable result. Put independent conditions or consequences on separate
`And` lines. Begin each step with the exact `Given`, `When`, `Then` or `And`
keyword followed by a space, without intervening punctuation or conditional
prose. Bind varying preconditions to concrete example columns or separate cases.
Cover supported success, failure and boundary behavior; give each
scenario a stable kebab-case id. Write each edge case as its own scenario: a
boundary, a rejected input, a conflict or an exception to the normal flow.
When the reason for the rule is known, state it in the scenario.
State the permissions, flag states and boundaries that distinguish success
from failure. Every example row must satisfy all of its scenario's `Given`
preconditions. Compare related scenarios so identical conditions do not promise
contradictory results.

**Decisions.** Record each design or architecture decision as a decision
record in the ADR directory (`docs/adr` unless `.facetrc.yaml` sets
`adr.directory`): its context, the decision and the reason for it. List the
file in `intent.decision_records` of every record it shapes. The record keeps
the summary; the decision record keeps the reasoning.

Choose the vocabulary of the actual product surface. A unit test asserting
`access_until == period_end` can prove that access continues until the paid
period ends; it does not make the assignment the user's contract. An internal
`PermissionError` can prove a cancellation is rejected without changing the
subscription; it does not establish that the product exposes that exception.
Read callers and entry points to establish the public behavior. State exact
exceptions, protocol statuses or exit codes only when that interface is itself
the product being described and the evidence establishes them. Developer tools
may need engineering language; do not turn internal code into everyday metaphors.
Do not enumerate every internal branch as a contract case. A parser exception
or missing dictionary key is not automatically a supported rejection flow.
Separate declared product behavior from incidental failure modes; record a
confirmed unwanted behavior as a `deviation` when applicable, or flag the
uncertain contract in the handover. A thrown exception alone does not prove
"no changes": check mutation order and assertions before promising preservation.
Use `pre_state` and `post_state` for useful implementation precision beside
readable statements, and classify `audience` according to the statement when useful.
Engineering contracts may describe supported operational behavior, including
telemetry; name the observable guarantee rather than merely a function call or
code branch.

When supported cases share a rule and differ only in data, use a parameterized
statement with schema `examples` blocks: `<column>` placeholders, `columns`,
and `rows`. Name blocks by their rule; keep each row the width of its effective
columns. Columns may inherit from the first declaring block. Include evidenced
values and boundaries, not invented permutations. Distinct rules need distinct
steps or scenarios; do not force unrelated behavior into an outline.
Example cells substituted into literal commands must contain actual argument
values, not explanations; an empty cell may represent an omitted option.
Put explanations outside such cells.
Every `pre_state` and `post_state` claim must hold for every represented case;
qualify case-specific claims explicitly or model them in separate scenarios.

**Verification.** Read the exact test bodies and assertions, not just names or
search hits. Account for every existing test relevant to the feature's behavior,
including operational and telemetry assertions. Where such a test establishes
a supported guarantee, include that guarantee and its verification; explain any
relevant test left unreferenced in the handover. For file-based tests, use
`id: path/to/test_file::exact_selector`
(including a class when required) so the reference selects the actual test.
Choose the schema's matching test category. Map `proves` only to scenario ids
whose conditions and consequences the assertions establish; for an outline,
check its represented cases. Before setting `proves`, establish `Given` conditions
and `pre_state` through controlled test setup or assertions. Find exact assertions,
including their selectors, for every `Then`, subsequent `And` result and
`post_state` claim. Production code alone is not test proof. A metric-value
assertion alone does not prove its kind or labels; specific type or label
selectors or assertions can establish them. Limit a tested
operational scenario to the guarantees asserted; retain source-supported kinds
and labels in structured `observability`. A similarly named test, a fixture or
an unasserted call is not proof. Narrow or split partially proved scenarios when
that reflects distinct behavior, otherwise report the proof gap briefly outside
the YAML rather than setting `proves`. Unit assertions can prove product behavior without
becoming product wording. Keep uncovered scenarios without invented test links;
report proof gaps and distinguish tests read from tests actually run.
Before finishing, cross-check every asserted case or branch in each relevant
test against all applicable scenarios; map every scenario it fully proves,
rather than stopping at the first case or variant. Recheck every outcome,
including supplemental `post_state`: remove unasserted extra invariants from
test-proved scenarios, or document useful source-grounded behavior separately
without claiming proof. Ensure example columns that vary conditions or actions
are bound to step placeholders; explanatory columns need not be bound.

**Complete the applicable sections.** Inspect every available schema section
against evidence, rather than stopping at the required fields. Include useful
facts already present in sources:

- `intent.decision_records`, scenario states, variants, `backing` and `deviation`.
- `verification` and all five `knowledge` slots as described above.
- `observation`: configured SLOs, alerts, dashboards and continuous checks.
- `observability`: actual metric, span and structured log emission contracts.
- `flags`: real exposure controls; `defects`: supported fix attribution/history.
- `lineage`: evidenced owner, creation date, dependencies and related policies,
  assumptions or data entities. Do not confuse record creation with commit dates.
- `authorship`: applicable draft provenance and human review requirements;
  `metadata`: short non-normative annotations only.

Use each field's existing schema shape. Optional fields without relevant evidence
may remain absent: no empty decorative blocks, guessed telemetry, copied sibling
facts or new schema keys. A completeness pass finds useful evidence; it does not
require every optional key. Keep uncertainties and concise review requests in
the handover rather than turning the record into an investigation transcript.

Use exactly one lifecycle representation. `planned` is for an unimplemented
feature; promote it to `active` with its `@facet:` anchor in the same change.
Existing variant registries use their per-variant lifecycle map. Resolve
`owner.team` from explicit ownership evidence; if unavailable, leave it unknown
and report the missing required field.

## Changing an existing record

A behaviour change updates the record in the same change, not in a follow-up
(FACET spec §12.1). Check specifically whether the change invalidates a
populated Knowledge answer — if it does, that answer is now wrong and the owner
must revise it. Flag it; do not silently rewrite it.

Refactors that preserve behaviour need no record edit, only anchor updates if
files moved.

## Finish

Run `facet-lint` and report what it says.

After an adoption sprint, or any session that added several records, also run
`facet calibrate`. It answers the question no per-record check can — not "is
this record good" but "have we been documenting at the right level at all" — and
its reading line names the level if the registry has drifted to one side.
