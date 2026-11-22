# @@PACKAGE@@

@@CORE@@
The native `facet` binary for `@@PLATFORM@@`. Use `facet-framework` to run
FACET; npm installs this package for you on matching machines.

```sh
npx facet-framework@@@VERSION@@ init
```

Documentation: <https://github.com/facet-framework/core>
@@/CORE@@
@@OTHER@@
The @@PRODUCT@@ native binary for `@@PLATFORM@@`, and nothing else.
@@/OTHER@@

## About this package

This package is an optional dependency of `@@LAUNCHER@@`, which picks the
platform package matching the machine it runs on. Do not install it directly.
The binary is the same file as in the release archive of the same version; its
SHA-256 is recorded in that release's `release-manifest.json`.
