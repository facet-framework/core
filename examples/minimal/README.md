# A minimal consumer registry

This is fictional data, not a real project's history. The record, source anchor
and test reference describe the same small task-title operation. The example's
schema is an exact copy of the schema shipped with this Core release.

From the Core repository root after building the command:

```sh
./dist/facet --root examples/minimal lint
go test ./examples/minimal/src
```

Change the function, run its tests and update its record when its behavior
changes. `proves` associates tests with promises; it does not execute them.

To use the layout in another project, copy `.facetrc.yaml` and `docs/facets/`,
then replace the fictional feature and its `src/` anchors with your own product
capabilities. Keep test IDs relative to your project root. Core can validate
records for languages other than Go; this example uses Go only to keep its
test execution self-contained.
