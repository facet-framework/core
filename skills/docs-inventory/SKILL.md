---
name: docs-inventory
description: Inventory FACET evidence for topic-based product documentation, identifying product areas, customer impacts and Knowledge gaps.
---

# Inventory the product before writing

Run `facet docs site inventory` from the project root. This loads every declared
registry and record category with registry-qualified ids, anchored paths and
SHA-256 digests. Use a task-owned temporary directory and remove raw inventory
after the accepted configuration and report are saved.

Read the records and relevant anchored sources. Produce an evidence matrix:
feature id, product topic, actual UI entry, prerequisites, observable result,
customer groups established by scenario constraints or product documentation, owner, declared metrics,
test references and unresolved Knowledge questions. Separate implemented,
planned, deprecated and variant-specific behavior. Never interpret a test
reference as a successful test execution or a metric declaration as a live SLO.

Scenario `audience` describes product/engineering readership; it is not a
customer tier. Keep unknown customer impact explicitly unresolved. Do not infer Free/Paid or
customer tiers from suggestive names. Carry policy, assumption and data-entity
constraints into the editorial evidence plan. Use `facet:authoring` upstream to
draft missing Knowledge answers; generation itself cannot invent them.

Handoff: a compact matrix, source ids, unresolved questions and proposed audience
mappings for `docs-plan`. Do not publish raw internal inventory to a public host.
