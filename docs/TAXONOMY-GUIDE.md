# FACET Taxonomy Guide

Practical guidance for declaring the two upper levels of your feature ids: the
product domains, and the capabilities under them.

The normative rules are FACET spec §4.4, and the sizing test that decides the
third level is §2.1.1. This guide is how to apply them.

---

## What the file is for

Every FACET id is `domain/capability/feature`. That is a three-level product
decomposition and it has always been there. What was never there is any
statement of which domains and capabilities exist — so the first two segments
were free text, could not be wrong, and were therefore never right.

The measured cost of leaving them free: comparable products, documented by the
same tooling in different repositories, came out roughly a hundred times apart
in record count. One had written a record per behaviour. The other had written a
record per product area. Neither broke a rule.

`docs/facets/taxonomy.yaml` is the rule.

```yaml
schema_version: "0.1"
domains:
  library:
    title: Library
    purpose: Everything a listener has deliberately kept.
    classification: core
    owner:
      team: library
    capabilities:
      my-library:
        title: My Library
        purpose: The listener's own saved tracks, albums and artists.
      playlist:
        title: Playlists
        purpose: Ordered collections a listener creates and edits.
      shared-playlist:
        title: Shared playlists
        purpose: Playlists with more than one editor.
        classification: supporting
```

An id then has to resolve against it. `library/playlist/playlist-authoring` is
accepted; `libary/playlist/…` is a lint error rather than a new domain nobody
noticed being created.

**Optional.** A project without the file gets no `taxonomy` finding at all —
the same rule `variants.yaml` has carried since 1.4.

One check does not wait for the file: `sizing` reports an active record with a
single contract scenario, or with more than twelve, in every project. That is
deliberate — gate 3 is the one part of the sizing test a machine can evaluate,
and a project that has not declared a taxonomy is exactly the project most
likely to need it. It is a warning, so nothing it says can fail a build.

---

## How to find your taxonomy

Not from the code. The directory structure of a codebase reflects how it was
built — by layer, by framework convention, by whoever started it — and a
taxonomy that mirrors it inherits all of that.

Three questions, in this order:

**1. What would a user name?** Not your services, not your repositories. The
areas someone would point at in the product and say "that part". For a music
service: authentication, library, player, explore. For a bank: accounts,
payments, cards, lending. This is the domain level and there are usually between
three and twelve of them.

**2. Inside each, what are the stable noun groups?** Under `library`: my
library, playlists, shared playlists. These outlive redesigns — a playlist is a
playlist whether it is a screen, a tab or a voice command. That is the
capability level, two to ten per domain.

**3. Everything below that is a record, and §2.1.1 sizes it.** If you find
yourself wanting a fourth level, you have found scenarios. Write them in the
record's contract.

A useful sanity check at the end: read the tree aloud as
"*domain* → *capability* → *feature*" and see whether it sounds like a product
or like an org chart. It should sound like a product.

---

## When a taxonomy is wrong

A taxonomy can be tidy, consistent, fully populated, and still wrong. Five ways:

**It mirrors the org chart.** `platform`, `growth`, `core-services` are teams.
Teams reorganise every year or two; ids are immutable for the life of the
product. An id built from the org chart is scheduled to be wrong, and the
`superseded_by` churn to fix it is the most expensive mistake on this page. The
team belongs in `lineage.owner.team`, where it can change freely. A domain may
name an `owner.team` of its own, and that is a statement about the domain — read
by people deciding who to ask, not substituted into the records underneath,
which each carry their own.

**It mirrors the architecture.** `frontend`, `backend`, `jobs`, `api`. These are
layers. A feature is a horizontal slice through them, so every record ends up
under whichever layer its author happened to be looking at, and the same feature
lands in different domains depending on who wrote it.

**It has one domain.** If thirteen of fourteen records sit under one domain, the
first segment distinguishes nothing and the reader gets a flat list where the id
promised a decomposition. `facet calibrate` reports this.

**It has a capability per feature.** The opposite failure: the capability holds
exactly one record, because the capability and the feature are the same thing
named twice at two levels — `agent-heartbeat/agent-heartbeat`. The middle
segment is being used as a namespace per feature rather than as a grouping.
Reported by `facet calibrate`.

This one needs care, because **a one-record capability is not wrong by itself**
and no threshold can tell the two apart. A capability that genuinely holds one
feature today and will hold three next quarter is correctly cut and simply
early; a taxonomy is allowed to describe a product area before the product
fills it. The count is the symptom. The tell is the name:

- **Read the two segments aloud.** If the capability segment is the feature
  segment again — verbatim, an abbreviation of it, or a synonym — there is no
  grouping there. That is a second name for one thing, and folding it costs
  nothing but the id.
- **Ask what the second record under it would be.** If you can name one, the
  capability is a grouping that happens to hold one member, and it stays. If
  the honest answer is "there won't be one, this *is* the feature", the segment
  is a namespace.
- **Look at the siblings.** A thin capability whose feature plainly belongs
  beside an existing capability's records is the fold; one that sits in a part
  of the product nothing else covers is a real area you have only started.

Where the segment is a second name, fold the record into the sibling capability
it belongs beside. Ids are immutable, so this is a new id with `superseded_by`
on the old one — which is why the last cheap moment is before the first record
lands (see *Changing a taxonomy* below).

**It is two taxonomies at once.** The one above in part of a registry and not
in the rest, which is hard to see from inside because the healthy half is
genuinely healthy. Part of the product is grouped properly — capabilities
holding twenty or thirty records each — and beside it runs a long tail where
every new feature arrived with a capability of its own. Measured once, on a
large registry outside this project: 171 of 265 capabilities held a single
record while three others held 36, 35 and 24. Whether that mixture is typical
is unknown — one registry cannot say — but it is the shape to check for,
because a median alone cannot adequately describe its mixed populations. The
median there was 1, which is true of the tail and a false impression of the
rest of the registry.

So read the distribution, not the median: `facet calibrate` names the largest
capability beside the count of thin ones for exactly this reason. And note what
the mixture means before you act on it — the project's record-writing is not
the problem, and the grouping practice it needs already exists in its own
registry. The work is the tail, one capability at a time, by the tell above.
Leave the rest alone.

---

## Classification: which parts are the point

A domain or capability may carry `classification`:

| Value | Means | What follows from it |
| --- | --- | --- |
| `core` | what the product is chosen for | custom work pays here; this is where your best people and your deepest records belong |
| `supporting` | necessary, specific to your business, not differentiating | build it competently, do not gold-plate it |
| `generic` | a solved problem you would happily buy | buy it; a thin record is appropriate |

This is Evans' core / supporting / generic subdomain from Domain-Driven Design,
in his vocabulary deliberately, so that everything already written about what to
do differently with each applies without translation.

**It is not depth.** This is the most common misreading, so it is worth being
blunt: `identity` sits at the top level of almost every taxonomy and is usually
`generic` — nobody chooses a music service for its login. A ranking model buried
at `explore/recommendation/…` may be the entire business. Position tells you
nothing about strategic weight, which is exactly why it has to be written down.

Depth, meanwhile, is never written down. It is the id. A record that also
carried its own level would be a second copy of a fact the id already states,
free to disagree with it.

**Absent means undecided, not `supporting`.** A capability with no
classification inherits its domain's, so the common case — a whole domain that
is generic — is stated once.

---

## Changing a taxonomy

Adding a domain or a capability is free. Removing one is free if nothing is
under it. Renaming or moving one is not free at all, because every record
beneath it carries the old segments in an immutable id.

So:

- **Add capabilities readily.** A capability declared before its first record is
  normal and produces only a warning, which clears when the record arrives.
- **Think hard before the first record lands under a new domain.** That is the
  last cheap moment.
- **When you must re-cut a domain**, the records under it get new ids, with
  `superseded_by` pointing forward from the old ones. Ids are never reused. This
  is expensive on purpose: it is the cost §2.1.1's tie-break exists to keep you
  from paying.

  Supersession preserves references to previous ids when a taxonomy changes.

If you are adopting FACET into a repository that already has records with an
inherited, accidental structure, declare the taxonomy that is *true today*
first — including the domain you are not proud of. A taxonomy that describes the
repository is useful immediately. A taxonomy that describes the repository you
wish you had makes every id an error on the day you commit it.

---

## The feature that fits nowhere

The most common question, and it has a counter-intuitive answer.

> I have a feature and none of the declared capabilities fits. Do I add one?

**Usually not.** Run the sizing test again first (§2.1.1). A feature that fits
no capability is far more often a feature sized wrong than a capability missing:

- Too fine — it is a scenario of a record that already exists, and the record it
  belongs to is already under a capability.
- Too coarse — it is itself a capability, and the features are the clusters of
  scenarios inside it.
- Named for its implementation — "the webhook retry queue" fits nowhere because
  it is a mechanism; the feature is "deliver a webhook reliably" and it sits
  under `notifications/webhook`.

Add a capability when the product genuinely gained an area, which happens, and
write its `purpose` line before you write the record. Having to write that
sentence is most of why the file works.

---

## Checking your work

```bash
facet lint        # taxonomy: ids resolve, declared nodes have records
facet calibrate   # the shape of the whole registry against the bands in §11.4
```

`facet calibrate` is the one to run after an adoption sprint. It answers the
question no per-record check can: not "is this record good" but "have we been
documenting at the right level at all".

---

## See also

- [`AUTHORING.md`](AUTHORING.md) — the sizing test, and how to write a record
- [`VARIANTS-GUIDE.md`](VARIANTS-GUIDE.md) — one feature existing in more than one place
- [`REGISTRIES-GUIDE.md`](REGISTRIES-GUIDE.md) — more than one product in one repository
- FACET spec §2.1.1, §4.4, §11.4
