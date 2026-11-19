---
name: taxonomy
description: Read, propose, or extend a project's FACET taxonomy (docs/facets/taxonomy.yaml) — the product domains and capabilities that the first two segments of every feature id are drawn from. Use when choosing where a new feature belongs, when a feature seems to fit no existing capability, when deciding whether something is one feature or several, when a project is adopting FACET and needs its domains mapped, or when the user asks about product structure, feature hierarchy, core versus supporting areas, or how to split a large area into features.
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Work with a FACET taxonomy

The canonical guide is `../../docs/TAXONOMY-GUIDE.md`; the
normative rules are FACET §4.4 and the sizing test is §2.1.1.

`docs/facets/taxonomy.yaml` declares the two upper levels of every id —
`domain/capability/feature`. Where it exists, an id naming an undeclared domain
or capability is a lint **error**. Where it does not, nothing here fires and the
project behaves as it always did.

## The rule that overrides your defaults

**You may propose a taxonomy. You may not declare one.**

Which domains a product has is a product decision, not an inference from a
directory tree, and it is the decision this whole mechanism exists to make
explicit. Inferring it puts the variance straight back where it was found —
that is the point of the file, not an obstacle to it.

So: draft it, show it, say what you based each node on, and let the owner
accept it. Do not write the file and mention it afterwards.

The one exception is a node the owner has already agreed to in this session.

## Reading it first

Before choosing an id for a new record:

1. Read `docs/facets/taxonomy.yaml` if it exists. The declared capabilities are
   the only valid second segments.
2. Choose the capability whose `purpose` line actually covers the feature. If
   two do, the boundary between them is unclear and that is worth saying.
3. If none does, go to the next section. Do not add one yet.

## "My feature fits no capability"

The most common question here, and the answer is usually not "add a capability".
A feature that fits nowhere is more often a feature sized wrong. Check, in this
order:

- **Too fine.** It is a scenario of a record that already exists — and that
  record already sits under a capability. Most "fits nowhere" features are this.
  Symptom: you can state only one or two things about it, and no failure path.
- **Too coarse.** It is itself a capability, and the features are the clusters
  of scenarios inside it. Symptom: you are writing more than nine scenarios and
  they have stopped referring to each other.
- **Named for its mechanism.** "The webhook retry queue" fits nowhere because it
  is a mechanism. The feature is "deliver a webhook reliably" and it sits under
  `notifications/webhook`.

Only when all three are ruled out has the product genuinely gained an area.
Then propose the capability **with its `purpose` line written** — having to
write that sentence is most of why the file works, and a node proposed without
one is a node nobody can evaluate.

## Proposing a taxonomy for a project that has none

Not from the code. A directory tree reflects how a codebase was built — by
layer, by framework convention, by whoever started it — and a taxonomy that
mirrors it inherits all of that.

1. **What would a user name?** The areas someone points at in the product and
   says "that part". Three to twelve of them. This is the domain level.
2. **Inside each, what are the stable noun groups?** Things that outlive a
   redesign — a playlist is a playlist whether it is a screen, a tab, or a voice
   command. Two to ten per domain. This is the capability level.
3. **Stop.** Everything below is a record, sized by §2.1.1. Wanting a fourth
   level means you have found scenarios.

Read the result aloud as *domain → capability → feature*. It should sound like a
product, not like an org chart.

Then present it with your reasoning per domain, and the parts you are least sure
of named as such. Do not write the file.

### Five ways a tidy taxonomy is still wrong

- **It mirrors the org chart** (`platform`, `growth`, `core-services`). Teams
  reorganise; ids are immutable. This is the expensive one — the team belongs in
  `lineage.owner.team`, where it can change freely.
- **It mirrors the architecture** (`frontend`, `backend`, `jobs`, `api`). Those
  are layers, and a feature is a slice through them, so the same feature lands
  in a different domain depending on who wrote it.
- **One domain holds almost everything.** The first segment then distinguishes
  nothing.
- **A capability per feature.** The capability and the feature are the same
  thing named twice (`agent-heartbeat/agent-heartbeat`) — the middle segment
  used as a namespace rather than as a grouping. Judge this by the **name, not
  by a count**: one record under a capability is fine if you can name the second
  record that will join it, and is a namespace if the honest answer is "there
  won't be one, this *is* the feature". Never report a thin capability as a
  defect on the count alone.
- **Part of it is grouped well and part of it is not.** Hard to see from
  inside, because the healthy half is genuinely healthy: capabilities holding
  thirty records sit beside a long tail holding one each. Fold the tail into the
  siblings it belongs beside, by the name test above; leave the working part
  alone, and say so — the project's record-writing is not the problem.

`facet calibrate` reports the last three directly, and names the largest
capability beside the count of thin ones rather than the count alone. Read that
contrast, not the median alone: a median cannot adequately describe a mixed or
multimodal distribution.

## Adopting into a repository that already has records

Declare the taxonomy that is **true today**, including the domain nobody is
proud of. A taxonomy describing the repository is useful immediately; one
describing the repository the owner wishes they had makes every existing id an
error on the day it is committed.

Re-cutting comes after, deliberately, with `superseded_by` — ids are never
reused and never edited in place.

## Classification

A domain or capability may carry `classification: core | supporting | generic`
(Evans' subdomains). It records **strategic weight only**, and you should
propose it rather than assume it:

- `core` — what the product is chosen for
- `supporting` — necessary and specific to the business, not differentiating
- `generic` — a solved problem the owner would happily buy

**It is not depth.** `identity` sits at the top of most taxonomies and is
usually `generic` — nobody chooses a music service for its login — while a
ranking model three segments deep may be the entire business. Never infer it
from position, and never write it onto a record: depth is the id, and strategic
weight lives on the taxonomy node.

Absent means undecided, not `supporting`. A capability inherits its domain's
value, so a whole domain that is generic is stated once.

## Checking

```
facet lint        # ids resolve; declared nodes have records under them
facet calibrate   # the shape of the registry against the bands in FACET §11.4
```

A declared capability with no record under it is a **warning**, not an error —
declaring before implementing is the intended order. Report it; do not
"fix" it by deleting the node.
