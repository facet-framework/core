---
name: docs-site
description: Build and verify a deployable standalone documentation website from a FACET editorial configuration.
---

# Build and verify the product reference

Read `../../docs/DOCS-EXPERIENCE.md`. Validate the plan, build to a new output
directory, then run `facet docs site check` with the same configuration. Never
remove or overwrite an existing site to make a build pass.

Use a browser to check complete topic navigation and ungated perspective changes, mobile layout, keyboard navigation,
search (including empty results), article section links, source references and
SVG text alternatives. Every page should work with relative assets under a
hosting prefix and through a direct static file open. Search is progressive;
without JavaScript the complete reference stays readable.

Compare the complete artifact with its evidence: sources page, missing answers,
responsible team, declared metrics and scenario/test references. Internal visibility includes selected record fields and inventory. Public
visibility strips internal evidence; the private lock stays outside the deployed
directory. Select hosting access to match the content. Reader perspectives are navigation, not access controls.

Request the configured independent reviewer and human content acceptance. Keep
a compact result per run, reviewer findings and fixes. Publish or deploy only
within the user's authorized scope. Clean temporary output and browser state.
