# facet-framework

The `facet` command line on npm.

FACET keeps your product's features, code and tests from drifting apart. It
describes each named capability in a YAML record, connects the record to the
source that implements it with `@facet:` comments, and checks both directions.

## Get started

In the root of your project:

```sh
npx facet-framework@@@VERSION@@ init
npx facet-framework@@@VERSION@@ lint
```

`init` writes the record layout, schemas, specification and a CI workflow.
`lint` checks every record against its schema and the source it anchors.
`drift` lists anchored code that changed since the last commit without its
record.

To keep FACET in the project:

```sh
npm install --save-dev facet-framework@@@VERSION@@
npx facet lint
```

Requires Node.js 18 or later on Linux, macOS or Windows, x64 or arm64.

Documentation and release archives: <https://github.com/facet-framework/core>

## About this package

`facet-framework` runs the native `facet` binary for your platform; there is
no JavaScript reimplementation. Installed into a project or globally, it
provides two equivalent commands, `facet-framework` and `facet`.

The binary comes from `@facet-tools/core` and its platform packages, which npm
installs as optional dependencies. Installing with `--omit=optional` leaves
the binary out, and the command says so.

A project can pin its FACET version in `.facetrc.yaml` (`requires.core`). When
the installed version does not match, the command refuses and names the one to
use instead, for example `npx facet-framework@<pinned version> lint`.
