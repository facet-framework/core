# facet-framework

FACET Core — Feature-Anchored Canonical Executable Truth — as an npm package.
It runs the native Core binary for your platform; there is no JavaScript
reimplementation.

```sh
npx facet-framework lint
npx facet-framework drift
npx facet-framework version
```

Installed into a project or globally, it provides two commands that do the
same thing: `facet-framework` and `facet`.

## Pinned Core versions

A project can pin its Core version in `.facetrc.yaml` (`requires.core`). When
the installed version does not match, the binary refuses and names the
command to use instead, for example `npx facet-framework@<pinned version> …`.

## Commands and the other FACET packages

`facet-framework` is the only FACET package that provides the `facet`
command. `facet-tools`, the entry point for Atlas and further products,
provides only `facet-tools`, so both can be installed globally side by side.

The binary comes from `@facet-tools/core` and its platform packages, which npm
installs as optional dependencies. Installing with `--omit=optional` leaves
the binary out, and the command says so.

Requires Node.js 18 or later on linux, macOS or Windows, x64 or arm64.
