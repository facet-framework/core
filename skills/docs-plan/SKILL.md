---
name: docs-plan
description: Design a complete topic-based product reference and ungated reader perspectives from a FACET evidence inventory.
---

# Plan a complete product reference

Read `../../docs/DOCS-EXPERIENCE.md` and the evidence inventory. Use
`mode: reference`, `catalog: true`, and topic prefixes that cover the product.
Every feature must remain reachable from the complete catalog, independently of
reader perspective. Organize navigation by product topic, not a prescribed
reading sequence or an audience entry gate.

Declare reader perspectives in `audiences`: PM, support, new colleagues and
engineering where appropriate. Customer groups must be grounded in the product.
In reference mode these are context choices in the header; their feature lists
must not restrict shared articles or the catalog. Use article `perspective_notes`
for specific implications while retaining the complete common explanation.

Write direct answers to recognizable product questions, concept explanations,
reference tables and task instructions. Cover capabilities and limitations.
Give each article a topic, a concise answer in its description, and cited sections.
Use diagrams for real product relationships, never an automatically numbered
reading route. Keep missing owner knowledge visible as a gap, without inventing it.

Run `facet docs site plan --config <file>`. Resolve unknown ids, invalid topic
assignments, malformed tables and uncited content before writing.
