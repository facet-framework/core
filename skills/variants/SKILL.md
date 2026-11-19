---
name: variants
description: Set up or reason about FACET variants — repositories where the same feature exists more than once, such as a web and a native client, or a legacy system and its rewrite. Use when the user asks how to document a monorepo with multiple apps or platforms, when a migration or rewrite needs tracking, or when the linter reports a variant that is not declared.
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Variants: one feature, more than one place

The canonical guide is `../../docs/VARIANTS-GUIDE.md`. Read it
before setting anything up — this skill covers how to apply it in a session.

A **variant** is an independently deployable place where a feature can exist or
not exist: clients, but also backends, workers and pipelines. In a rewrite
everything duplicates, so `legacy-api` and `v2-api` are variants just as much as
`web` and `ios`.

## Establish the situation before writing anything

Four situations look alike and only one is variants. Getting this wrong is
expensive to undo, so settle it explicitly with the user rather than inferring:

| Situation | Model |
|---|---|
| Same feature, implemented twice | One record, two variants |
| Two genuinely different features | Two records |
| Two separate products in one repository | Two registries, separate `docs/facets/` trees |
| Same code, different configuration | Not a variant — a flag or a policy |

**The intent test decides it.** If one paragraph is honestly true of both
implementations, it is one feature. "Creates a checkout session" is true of the
web and the native implementation, and of the legacy and the rewritten one.

Staging versus production is never a variant: the same code is deployed to both.

## Setting up the map

Write `docs/facets/variants.yaml`. Derive the patterns from the repository's
actual layout — read it, do not assume `apps/web` exists because it usually
does.

Three rules to apply while writing it:

- **Longest matching pattern wins.** `packages/ui-web/**` beats `packages/**`
  regardless of order.
- **Patterns may match files.** A React Native project needs `**/*.ios.tsx`
  style patterns, because platform extensions share a directory and a
  directory-only map cannot separate them. Check for this — it is easy to miss
  and produces a map that looks right and classifies nothing.
- **Genuinely shared code goes under `shared`.** Libraries and infrastructure
  serving several variants. Two competing implementations of the same thing are
  *not* shared — a rewrite's old and new backend are two variants.

For a rewrite, set `replaces` on the new variant. It drives migration reporting
and no lint, so it costs nothing and makes progress countable later.

## Converting existing records

A record that gains variants **loses its top-level `lifecycle`** — they are
mutually exclusive. Do not leave both; the schema rejects it.

Ask the user for the per-variant states rather than guessing. "It is active on
web" is knowable from the code; "it is planned for iOS" versus "it will never be
built for iOS" is a product decision, and an absent variant means the second.

Never split a record because it has two variants.

## After setting up, expect new findings

Running the linter for the first time with a map usually surfaces real problems
that were previously invisible. Read each rather than silencing it:

- **`anchored file belongs to no declared variant`** — the map does not cover
  that path. Add a pattern, or move the file under `shared` if it belongs to no
  single variant.
- **`active on X but only shared code anchors it`** — usually the client-specific
  anchor is missing. Occasionally it means the feature is not actually reachable
  on that variant, in which case the lifecycle is wrong, not the anchor.
- **`anchored on variant X, which the record does not declare`** — someone
  implemented it there without updating the record. Add the variant.

## Verify

Run `facet lint` and report the result per variant. If the project is
mid-migration, say what is still on the old variant only — that number is the
point of the exercise, and it is the thing a single aggregate lifecycle used to
hide.
