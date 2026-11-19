---
name: docs-write
description: Compose grounded onboarding guides, PM and support documentation, FAQs and diagram concepts from FACET evidence.
---

# Write for the reader's next task

Read `../../docs/DOCS-EXPERIENCE.md` and the editorial plan. Follow the project's
prose conventions. Write a title that names the reader's outcome, a short purpose,
prerequisites, ordered actions where the sources actually supply them, an
observable success signal, and supported recovery advice. Cite each section's
record ids in `sources`. Use `getting-started`, `guide`, `feature` or `faq`.

`facet docs site compose --config <empty-articles-config>` can create conservative
seed articles using populated Knowledge answers. Improve their titles and
organization, merge related features into useful guides, and replace copied
technical framing with reader-oriented prose while preserving the source claims.
This seed is not a finished onboarding experience.

Empty answers stay absent. Do not derive a new answer from source code during
generation; draft that upstream through authoring first. Do not turn Gherkin
fixtures into product instructions. Preserve variant, lifecycle and audience
scope. Team, metrics and scenario/test metadata come from records; unknown values
stay unknown. Declared verification is distinct from executed verification. In German, what a record sets down is an *Anforderung* (the *SOLL*),
a scenario is an *Akzeptanzkriterium* (plural *Akzeptanzkriterien*), and an
entry under `observation.slos` is an *SLO*.

Plan diagrams where relationships, sequence or decisions become easier to
understand. The built-in SVG is a labelled map of guide sections, not proof of a
product workflow. For product decisions or system diagrams, use the article `diagram` nodes,
edges, sources, title and description with cited evidence and identify inference in the review handoff; never pass a
reading sequence off as product behavior. Keep a text equivalent and short labels.

Handoff the configuration, changes in structure, unresolved evidence and human
review status to `docs-site`. Generated prose remains a draft until accepted.
