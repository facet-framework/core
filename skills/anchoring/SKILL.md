---
name: anchoring
description: "Place or fix @facet: and @facet-role: tags in source files so code links back to its FACET record, in the comment style of the file's language. Use when implementing a feature that has a FACET record, when promoting a record from planned to active, when the linter reports an orphan record or an unresolved anchor, or when moving code that carries anchors."
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Place FACET anchors

The canonical placement rules per language are in
`../../docs/ANCHOR-GUIDE.md`. Read it for anything beyond the
summary below.

## Shape

Two lines, in the file's native comment style, `@facet:` first:

```
@facet: <domain>/<capability>/<feature>
@facet-role: primary | related | config | data
```

The id must be the three-segment form. A single-word tag like `@facet: auth` is
silently ignored by the linter — it will look like the anchor exists when it
does not.

## Choosing the role

- `primary` — the authoritative implementation. One or two files per feature.
  At least one `primary` anchor is required before a record may be `active`.
- `related` — supporting files. Use freely.
- `config` — feature flags, environment wiring.
- `data` — models, schema definitions, migrations.

## Placement

Immediately above the symbol that is the feature's entry point — the handler,
service, core function, or the struct that owns it. Put it at the top of the
file only when the whole file implements the one feature.

A file may carry anchors for several features. Prefer splitting the file, but
multiple anchors are valid.

A tag is a comment, and many languages turn the comment directly above a
declaration into that declaration's documentation. Keep the tag from running
into it:

- **Top of a Go file:** leave an empty line between the tag block and the
  `package` clause. A comment that touches `package` is the package's
  documentation, so `go doc` and pkg.go.dev would open the package with
  `@facet: …` lines.
- **Above a documented Go symbol:** an empty line between the tag block and
  the doc comment keeps the tags out of the symbol's documentation altogether.
  An empty comment line (`//`) instead keeps them in it, as a paragraph of
  their own rather than part of the first sentence.

```go
// @facet: payments/checkout/stripe-session
// @facet-role: primary

// Package checkout creates and expires checkout sessions.
package checkout
```

The linter finds the tag in either position; the empty line only decides what
readers of the generated documentation see.

## Promoting planned → active

1. Add `@facet:` + `@facet-role: primary` to the implementing file(s).
2. Change `lifecycle: planned` to `lifecycle: active` in the record.
3. Run `facet-lint`.

Both halves belong in the same change. A record flipped to `active` without an
anchor fails CI, and an anchor pointing at a record that does not exist fails it
just as hard.

## Repositories with variants

If `docs/facets/variants.yaml` exists, a file's variant comes from its path —
there is no variant tag, and the anchor syntax is unchanged.

The consequence that matters: **shared code proves no variant.** A feature
marked `active` on a client needs an anchor in a path exclusive to that client.
Anchor shared code as well, but do not treat it as evidence the client can reach
the feature. See `../../docs/VARIANTS-GUIDE.md`.

## Repositories with several registries

A monorepo may declare one registry per product in `.facetrc.yaml`. Ids are
global across them: the same id in two registries is the same feature with a
record in each. The tag syntax is unchanged, and a bare id is still correct
almost everywhere — it resolves against any registry that lists the file's
directory.

Prefix it only in code two registries share, where the same id exists in both:

```go
// @facet: storefront:auth/login/password
// @facet-role: related
```

A prefix naming an undeclared registry is an error — every registry skips such a
tag, so the file leaves the registry silently. See
`../../docs/REGISTRIES-GUIDE.md`.

---

## When code moves

Anchors travel with the code they mark. Moving a function to another file moves
its anchor. A refactor that preserves behaviour needs no record edit — but if
the record's `anchor` section lists explicit paths, update those.
