---
name: knowledge
description: Generate user-facing documentation from FACET records — help articles, FAQ entries, tooltips — and keep it fresh as the code changes. Use when the user asks to generate, write, or update help content, docs, or an FAQ from FACET records, or when 'facet docs check' reports artifacts that need regeneration.
---

Resolve relative resource paths below from this SKILL.md directory, not the
project working directory. Install the whole plugin to retain those resources.

# Generate documentation from records, and keep it fresh

Implements the §8.5 generator pipeline and the §8.6 generation freshness loop
of `../../docs/framework/FACET.md`.

The pipeline has six stages: load → resolve `derive_from` → compose → render →
review → drift-detect. The sixth is what makes the other five worth doing; an
article generated once and never revisited is a liability.

## Generating

**1. Load.** Read the record. `facet feature_get`-style access is available via
MCP, or read the YAML directly.

**2. Resolve `derive_from`.** Each Knowledge slot declares which sources feed
it. Load exactly those. `derive_from: [anchors]` means read the anchored source
files — use `facet` MCP's `anchor_list`, or `grep` for the id.

**3. Compose.** Write from the record and the sources you loaded. Every claim
must trace to one of them.

**4. Render.** Match the surrounding documentation's format and voice. Read a
neighbouring article first rather than inventing a house style.

**5. Review.** Publication is review-gated. You produce a draft; a human
accepts it. Say so when you hand it over.

## The rule that matters most: refuse rather than fabricate

**An empty Knowledge slot means the article does not cover that ground.** It
does not mean you should work the answer out.

The record is the reviewed source. An article generated from it inherits that
review; an answer you compose here while writing prose does not — it goes
straight to users with nobody having checked it. That is the whole reason the
restriction sits at generation time rather than at authoring time.

So when a slot is empty, **do not fill it in the article.** Leave that section
out, and list it as blocked. A shorter article plus a clear statement of what is
missing is the correct output. `facet lint` reports the gaps per record.

The fix is upstream, not here: get the answer into the record through
`/facet:authoring`, where drafting from the intent, the contract, and the
anchored code is legitimate and lands in a diff someone reviews. Then
regenerate. Offer that as the next step rather than treating the gap as
permanent.

What stays off-limits in both places is fabrication — a permission model nobody
read, a support pattern nobody observed. Plausible and unverifiable is worse
than absent, because absence is visible and plausibility is not.

## Recording provenance — do not skip this

After writing an artifact, record what produced it:

```
facet docs record \
  --feature <id> \
  --artifact <path to the generated file> \
  --slots <slots you actually used> \
  --model <the model you are> \
  --prompt-version <version of this skill's approach>
```

This writes the record and every anchored source file, with content digests,
into `docs/facets/generated.lock.yaml`. Commit it with the article.

Without it the article is untracked and nothing will ever notice when it goes
stale — which is the whole failure this exists to prevent.

## Regenerating

`facet docs check` reports artifacts whose inputs moved. For each one:

1. **Read the actual diff of the changed input** before deciding anything. A
   changed digest means "something moved", not "the article is wrong".
2. **If behaviour a user would notice changed** — regenerate the affected
   sections, then `facet docs record` again to refresh the digests.
3. **If the change was behaviour-preserving** — a refactor, a comment, a test —
   the article is still correct. Re-record to acknowledge the new input state,
   and say in your summary that you re-recorded without rewriting, so nobody
   later mistakes it for a regeneration.
4. **If a populated Knowledge answer is now wrong** — stop. That answer belongs
   to the owner. Flag it; do not rewrite it.

`orphaned` means the feature record is gone: the article describes something
that no longer exists in the registry. Ask before deleting it — the record may
have been renamed rather than removed.

## Verify

Run `facet docs check` and report the result. If artifacts remain stale by
design, say which and why, so the next run does not treat them as new.
