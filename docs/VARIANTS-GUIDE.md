# FACET Variants Guide

Practical guidance for repositories where the same feature exists more than
once: a web app and a native app, a legacy system and its rewrite, or both at
the same time.

The normative rules are FACET spec §2.4. This guide is how to apply them.

---

## What a variant is

**A variant is an independently deployable place where a feature can exist or
not exist.**

Clients qualify — `web`, `ios`, `android` — and so do backends, workers and data
pipelines. The test is not what a thing *is* but whether the question "does this
feature exist there yet?" has its own answer.

That matters most during a rewrite, where everything duplicates: `legacy/api`
and `v2/api`, `legacy/web` and `v2/web`, `legacy/pipelines` and `v2/pipelines`.
All of those are variants. None of them is shared code — shared is precisely
what two competing implementations are not.

---

## The decision that comes first

Before reaching for variants, work out which of four situations you are in.
Getting this wrong is expensive to undo, and three of the four are not variants.

| Situation | Model it as | Test |
|---|---|---|
| Same feature, implemented in two places | **One record, two variants** | Can you write one intent paragraph true of both? |
| Two genuinely different features | **Two records** | The intent paragraphs differ |
| Two separate products in one repository | **Two registries** | Different owners, no shared features, separate `docs/facets/` trees |
| Same code, different configuration | **Not a variant** | Only a config value differs — that is a feature flag or a policy |

**The intent test settles almost every case.** "Creates a checkout session" is
true of the web implementation and the native one, and true of the legacy
implementation and the rewritten one. So: one record. If you cannot write a
single paragraph that is honestly true of both, they were never one feature and
two records is correct.

Note what the test rules out. A web checkout that takes card payments and a
native checkout that takes Apple Pay are still one feature with one intent —
the payment method is a contract detail, expressed as a variant-narrowed
scenario, not a reason to split the record.

---

## Setting it up

Declare the repository's variants once, in `docs/facets/variants.yaml`:

```yaml
schema_version: "0.1"
variants:
  web:
    paths: ["apps/web/**", "**/*.web.tsx"]
    description: "The browser client."
  ios:
    paths: ["apps/mobile/ios/**", "**/*.ios.tsx"]
  android:
    paths: ["apps/mobile/android/**", "**/*.android.tsx"]
shared:
  paths: ["packages/core/**", "services/api/**"]
```

Three rules govern how a file is classified:

1. **The longest matching pattern wins**, as in routing. `packages/ui-web/**`
   beats `packages/**`, whichever order they appear in. Without this rule the
   file's order would decide the answer and two readers would disagree.
2. **Patterns may match files, not only directories.** This is not a nicety:
   React Native puts `Button.ios.tsx` and `Button.web.tsx` in the same
   directory, and a directory-only map cannot express the most common
   multi-variant layout there is.
3. **A file matched by two variants at the same specificity is shared.**
   Exclusivity is the test, so code serving both native clients belongs to
   neither exclusively.

### A rewrite

Everything duplicates, so name variants for the combination:

```yaml
schema_version: "0.1"
variants:
  legacy-api:
    paths: ["legacy/api/**"]
  v2-api:
    paths: ["v2/api/**"]
    replaces: legacy-api
  legacy-web:
    paths: ["legacy/web/**"]
  v2-web:
    paths: ["v2/web/**"]
    replaces: legacy-web
shared:
  paths: ["packages/**"]
```

`replaces` records that one variant is superseding another. It drives reporting
— how far the migration has got, what still blocks retiring the old variant —
and deliberately drives no lint. A check that fired on every not-yet-migrated
feature would produce a warning per record for the duration of the migration,
and a signal that loud stops being read.

---

## Writing a record with variants

A record that spans variants replaces `lifecycle` with a per-variant map. The
two are mutually exclusive: a record has one or the other, never both.

```yaml
schema_version: "0.1"
id: payments/checkout/session
variants:
  web:
    lifecycle: active
  ios:
    lifecycle: planned
    notes: "Blocked on the native payment sheet."
  # android is absent — the feature does not apply there
```

**An absent variant means not applicable, and is not a gap.** A web-only
feature lists only `web`. You never have to enumerate every variant in every
record.

The aggregate is gone on purpose. A single `lifecycle` on a feature that is live
on web and unstarted on native has to claim one of the two, and whichever it
claims is false about the other half. Every number computed from it inherits
that — a registry reporting "80% of features active" while the native client is
at zero is worse than no number, because it is believed.

### Behaviour that differs per variant

Narrow the scenario rather than splitting the record:

```yaml
contract:
  scenarios:
    - id: session-created
      statement: |
        Given a plan
        When the customer subscribes
        Then a checkout session is created
    - id: biometric-reauth
      variants: [ios, android]
      statement: |
        Given the session has expired
        When the user returns to the app
        Then re-authentication is offered biometrically
```

A scenario with no `variants` applies everywhere the feature exists.

### Knowledge that differs per variant

Any slot may carry per-variant overrides. The slot's own answer is the shared
default; an override replaces it for that variant only.

```yaml
knowledge:
  discoverability:
    question: "Where does the user enter this flow?"
    by_variant:
      web: { answer: "Settings → Billing → Change plan" }
      ios: { answer: "Tap ⋯, then Billing" }
  not_for:
    question: "When is this the wrong tool for the job?"
    answer: "Not for invoiced enterprise contracts — use the quote engine."
```

`discoverability` is the slot that almost always differs, because it is a UI
path. Most others are shared. An absent override is not a gap; it means the
shared answer applies.

---

## Anchoring

**There is no variant tag.** A file's variant comes from its path, and the
`@facet:` and `@facet-role:` tags are exactly as they were.

This is deliberate. A `@facet-variant:` annotation could contradict the
directory the file sits in, and then two sources of truth disagree about the
same file. A path map cannot lie about where a file is, and it costs nothing to
maintain per anchor.

### What shared code proves

**Nothing about any variant.** This is the rule most likely to surprise you.

Shared business logic existing does not mean a user of the native app can reach
the feature — the screens may not exist yet. So a variant counts as implemented
only when an anchor sits in a path belonging to **exactly one** variant.

Anchor shared code anyway: it resolves for navigation, hover, and drift
detection, and it is genuinely part of the feature. It simply does not answer
the question "can someone on this client use it".

A variant marked `active` whose only anchors are shared produces:

```
warning: "payments/checkout/session" is active on "ios" but only shared code anchors it
```

Read that as a question, not a defect: is there really an iOS screen calling
this, and is it anchored? Usually the answer is that the anchor is missing.

### Keeping the map honest

An anchored file matching no declared pattern is an **error**:

```
error: apps/desktop/checkout.ts: anchored file belongs to no declared variant
       and no shared path
```

Without that check the map quietly stops covering new packages as the repository
grows, and coverage under-reports with nothing turning red.

---

## For agents working in these repositories

Five rules, in the order they are most often broken.

1. **Never split a record because a feature has two variants.** One feature is
   one record. Splitting duplicates the intent and the contract, and the two
   copies drift — which is the exact failure FACET exists to prevent.

2. **Never invent a variant name.** Use the ones in `variants.yaml`. A record
   naming an undeclared variant is a lint error, and inventing one to make a
   check pass hides the fact that the map needs updating.

3. **Promote a variant to `active` only with an anchor exclusive to it.** Adding
   the lifecycle without the anchor produces a red build; adding it because
   shared code exists produces a warning that is telling you something true.

4. **When implementing on a new variant, update the record in the same change.**
   Anchoring a feature on a variant the record does not declare is reported —
   correctly — as someone implementing without telling the record.

5. **Do not answer `by_variant` Knowledge slots you cannot ground.** The
   per-variant override follows the same rule as every other owner-supplied
   answer: draft from the intent, the contract and the anchored code, or leave
   it and say what the owner has to supply.

---

## Common mistakes

**Using variants for configuration.** Staging and production are not variants —
the same code is deployed to both. A variant exists when the *code* can exist in
one place and not the other.

**Using variants where a registry belongs.** A marketing site and an admin tool
in one monorepo are separate products with separate owners and no shared
features. They get separate `docs/facets/` trees, not variants of each other.

**Modelling a rewrite as two registries.** Tempting, because the arithmetic
looks clean — and wrong, because it duplicates every record during exactly the
period when two implementations are being maintained in parallel and drift is
most likely. A rewrite is variants of one registry, and per-variant lifecycle
gives you the migration progress for free.

**Narrowing the scan and forgetting CI.** `FACET_SOURCE_DIRS` set locally but
not in the workflow means the two scan different trees and disagree about which
records are orphaned — and the local run is the one that looks fine.

**Treating a green lint as a migrated system.** The linter checks that records
and code agree. A feature `active` on `legacy-api` and absent from `v2-api` is
perfectly consistent and completely unmigrated.
