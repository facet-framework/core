# Product documentation from FACET

The reference experience uses persistent topic navigation, global search, a
complete feature catalog and a reader-perspective selector. Changing perspective
adds relevant context; it never hides shared articles or product features.
There is no required reading sequence.

A site is described by an editorial JSON configuration, `docs/site.json` unless
`--config` names another file. A reference site sets `mode: "reference"` and
`catalog: true`, names its `language`, and declares `visibility: "public"` or
`"internal"`; an internal site is meant for review by the project, not for public
hosting.

Seed, validate, build and verify it with `facet docs site`:

```sh
facet docs site compose --config docs/site.json   # draft articles to review
facet docs site plan    --config docs/site.json   # validate the configuration
facet docs site build   --config docs/site.json --output /tmp/facet-docs
facet docs site check   --config docs/site.json --output /tmp/facet-docs
```

The build output must not already exist. The digest lock lives beside it, outside the
publishable directory. HTML, CSS, JavaScript, search data and authored SVG diagrams
work without a running application server. Search loads a local script instead
of fetching JSON, including when opening the files directly.

## Editorial configuration

- `topics`: stable id, title, description and record-id prefixes. First matching
  prefix wins; unmatched features appear in an additional topic.
- `audiences`: perspective options, with id, title, kind and description. Reference
  articles may omit `audience`; their evidence is independent of these options.
- `catalog: true`: include every feature record visible under the export policy.
  Internal sites show declared lifecycle states; public sites include active only.
- `articles`: topic, title, direct-answer description, tags, cited feature ids and
  sections. Sections support prose, lists, code and rectangular reference tables.
- `perspective_notes`: additional explanation with perspective id and cited sources.
  The shared article remains visible in every perspective.
- `feature_labels`: optional editorial titles and descriptions keyed by source id.
  Without them the catalog uses the record summary and a readable id-derived title.
- `diagram`: explicitly authored nodes, edges, description and source ids. The
  renderer supplies an accessible SVG and equivalent text; it invents no diagram.

Source citations, team ownership and declared verification remain inspectable.
A declared test association is not a claim of successful execution. Empty
Knowledge answers are not replaced by invented explanations. The conservative
`compose` operation can seed topic articles, but requires editorial review.

Legacy configurations without `mode` keep their audience-scoped composition and
validation. New product documentation should use reference mode.

## Publication and maintenance

Public mode strips source paths, raw metadata and internal provenance from its
inventory projection. Public catalog entries use summaries and editorial labels; raw Knowledge answers
remain internal unless explicitly incorporated into an article. Review editorial
text for the intended readers. Perspective selection is navigation, never
an access-control mechanism. Choose hosting access to match the bundle.

`check` compares the source/configuration digests, output file set and digests,
then independently renders again. Changed inputs require a content review and
fresh build. Keep only the current preview; remove temporary builds after use.

The private `<output>.lock.json` has mode 0600. Never publish it or put generated
locks in version control; deploy only the site directory. Keep the deployed site
until its replacement passes review. IDs use lowercase letters, digits and hyphens.
Diagram labels are limited to 38 characters and positions to x=8..480/y=8..1800.
Use `plan` to validate the editorial configuration and `compose` to seed an empty
configuration. Article kinds also include `overview` and `concept`.

The header theme switch offers light and dark reading palettes. The initial theme
follows the operating system; a manual choice persists in local browser storage
and takes precedence on subsequent pages. Without JavaScript the CSS follows the
system preference. `theme.js` runs before the stylesheet to avoid a wrong-theme
flash; include it when deploying the standalone bundle.
