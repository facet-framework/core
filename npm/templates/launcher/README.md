# @@PACKAGE@@

@@CORE@@
The native binary launcher behind `facet-framework`. Use `facet-framework` to
run FACET; this package has no command of its own.

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
@@/CORE@@
@@OTHER@@
Launcher for the @@PRODUCT@@ native binary. It is a library for the entry
packages and has no command of its own.
@@/OTHER@@

## About this package

`@@PACKAGE@@` lists one platform package per supported platform as an exact
optional dependency — `@@PACKAGE@@-linux-x64`, `-linux-arm64`, `-darwin-x64`,
`-darwin-arm64`, `-win32-x64`, `-win32-arm64` — and npm installs the one that
matches the machine. The launcher runs that binary with every argument
unchanged, stdin, stdout and stderr inherited, and exits with the binary's exit
code. On Linux and macOS it passes SIGINT, SIGTERM and SIGHUP on to the binary;
on Windows the binary receives Ctrl-C from the console itself.

If the platform package is missing, optional dependencies were omitted
(`--omit=optional`, `--no-optional`) or `node_modules` was installed on another
platform and copied. Install again on this machine without omitting them.
