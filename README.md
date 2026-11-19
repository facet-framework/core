# FACET

[![CI](https://github.com/facet-framework/core/actions/workflows/ci.yml/badge.svg)](https://github.com/facet-framework/core/actions/workflows/ci.yml)

Version **0.1.0-rc.1**. The specification in `docs/framework/FACET.md` is
edition **0.1.0**; FACET releases and the specification are versioned
separately.

This is a release candidate. Commands, flags, Go APIs, MCP tools and schemas
may still change before 0.1.0.

Project website and documentation: <https://facet-framework.com>.

FACET describes a named product capability in a YAML record and connects that
record to source code with `@facet:` comments. This repository contains the specification,
the JSON schemas, the `facet` command that reads and checks those records, and
MCP and language-server interfaces over the same registry.

## Why use FACET?

Keep a capability's purpose, behavioral promises and verification references
together instead of reconstructing them from scattered implementation files.
FACET checks that records and tagged source remain structurally connected; it
does not establish test execution or semantic correctness on its own.

## Start in your project

From the root of your project, install the FACET layout:

```sh
npx facet-framework@0.1.0-rc.1 init
```

`init` writes the record directories, schemas, specification, lint tools,
dispatcher, `.facetignore` and CI workflow. Existing files are kept unless you
pass `--force`. It does not write your first feature record.

Choose one capability your project already implements. Create
`docs/facets/features/<domain>/<capability>/<feature>.yaml` with
`schema_version: "0.1"`, its three-segment `id`, an intent summary, contract
scenarios, an owner team and creation date. Use `lifecycle: active` and add a
matching `@facet: <id>` comment to the source that implements it. Use
`lifecycle: planned` for a capability whose implementation does not exist yet.
The [authoring guide](docs/AUTHORING.md) explains the fields; the
[minimal record](examples/minimal/docs/facets/features/demo/tasks/create-task.yaml)
shows a complete example.

If you use the plugin described below, the `authoring` skill drafts records
from the implementation and tests for human review. The `anchoring` skill
places source comments; the `lint` skill runs the checks and addresses findings.
These skills are installed with the plugin, separately from `facet init`.

Check the record and its source anchors:

```sh
npx facet-framework@0.1.0-rc.1 lint
```

For a different record directory or multiple registries, configure
[`.facetrc.yaml`](docs/REGISTRIES-GUIDE.md).

## What this repository contains

- The [`facet` command](cmd/facet/) and the Go packages behind it.
- The [specification](docs/framework/FACET.md) and [JSON schemas](schema/).
- A Claude Code and Codex plugin: [skills](skills/), [agent profiles](agents/),
  [hooks](hooks/hooks.json), MCP and LSP configuration, and the
  [`bin/facet` dispatcher](bin/facet) that installs a release binary.
- [Examples](examples/).
- This repository's FACET registry ([`.facetrc.yaml`](.facetrc.yaml),
  [`docs/facets/`](docs/facets/)), checked by CI
  with the command built from this tree.

## Install

### With npm

The npm package `facet-framework` runs the native FACET binary for your
platform; there is no JavaScript implementation. It needs Node.js 18 or later
on Linux, macOS or Windows, x64 or arm64. Release candidates are published
under the npm tag `next`, so name the version:

```sh
npx facet-framework@0.1.0-rc.1 init
npx facet-framework@0.1.0-rc.1 lint
```

Installed into a project or globally, the package provides two equivalent
commands, `facet-framework` and `facet`:

```sh
npm install --save-dev facet-framework@0.1.0-rc.1
npx facet lint
```

The binary comes from `@facet-tools/core` and its platform packages
(`@facet-tools/core-<platform>`), which npm installs as optional dependencies.
Installing with `--omit=optional` leaves the binary out, and the command says
so.

### With the dispatcher

[`bin/facet`](bin/facet) runs a matching native binary on `PATH` or downloads
and checks a platform archive against the release's `checksums.txt`. Downloads
are cached under `${XDG_CACHE_HOME:-$HOME/.cache}/facet/dist/core/`.

```sh
git clone --branch v0.1.0-rc.1 https://github.com/facet-framework/core.git
cd core
bin/facet version
bin/facet setup link --candidates
```

Downloads need `curl` or `wget` and `sha256sum` or `shasum`. Windows also
needs a POSIX shell and `unzip`. `setup link --dir <path>` creates a link in
a supported directory on `PATH`; `--candidates` lists those directories.

Each release on
[facet-framework/core](https://github.com/facet-framework/core/releases)
carries one archive per platform, named `facet_<version>_<os>_<arch>`:
`.tar.gz` for Linux and macOS, `.zip` for Windows. Each archive holds the
`facet` binary (`facet.exe` on Windows), `LICENSE`, `NOTICE`, this README,
`CHANGELOG.md`, the JSON schemas and the third-party notices. The release also
carries `checksums.txt` (SHA-256 of every archive) and `release-manifest.json`.

### Manual download

On Linux or macOS, replace `linux_amd64` with your platform (`linux_arm64`,
`darwin_amd64`, `darwin_arm64`):

```sh
version=0.1.0-rc.1
archive="facet_${version}_linux_amd64.tar.gz"
base="https://github.com/facet-framework/core/releases/download/v${version}"
curl -fsSLO "$base/$archive"
curl -fsSLO "$base/checksums.txt"
grep "  $archive\$" checksums.txt | sha256sum --check   # macOS: shasum -a 256 --check
tar -xzf "$archive" facet
./facet version
```

On Windows, download `facet_<version>_windows_amd64.zip` or
`facet_<version>_windows_arm64.zip`, compare its SHA-256 with the line in
`checksums.txt`, and extract `facet.exe`.

### Build from source

Use the Go version declared in `go.mod`:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-X main.version=0.1.0-rc.1" -o dist/facet ./cmd/facet
./dist/facet --help
./dist/facet --root examples/minimal lint
./dist/facet --root /path/to/your/project lint
```

```sh
go test ./...
go vet ./...
```

## Platforms

Release archives are built for Linux, macOS and Windows on amd64 and arm64.
They do not all receive the same checks:

| Platform | Go build, vet and tests | Release archive check |
| --- | --- | --- |
| Linux amd64 | yes (also with the race detector) | installed through `bin/facet`, `version --json` |
| Linux arm64 | no | installed through `bin/facet`, `version --json` |
| macOS arm64 | build and vet | installed through `bin/facet`, `version --json` |
| macOS amd64 | no | installed through `bin/facet`, `version --json` |
| Windows amd64 | build and vet | `facet.exe version --json` only |
| Windows arm64 | no | `facet.exe version --json` only |

Every package is also cross-compiled for all six platforms. The test suite
runs on Linux amd64 only; on the other platforms the binary is only checked to
start and report its build. The `bin/facet` dispatcher is not tested
on Windows.

## Commands

`facet --help` lists every command, and `facet <command> --help` its options.
The main ones:

- `init` scaffolds FACET into a project: the record layout, the schemas, the
  specification, the lint suite with a dispatcher pinned to this release, an
  empty `.facetignore` and the CI workflow. Everything it writes is carried in
  the binary, so it needs no source checkout and no network; files the
  project already has are kept unless `--force` is given.
- `lint` checks records against their schema and against the source they
  anchor. A failing lint reports invalid records or structural mismatches
  between records and source.
- `drift` reports anchored source that changed without its record.
- `test` runs the test commands a project declares and reports per-file
  results; see `examples/test-runners`. A test reference in a record alone does
  not establish that the test ran or passed.
- `docs site` plans, checks and builds a standalone documentation site from
  the registry and a reviewed editorial configuration.
- `mcp` and `lsp` serve the registry to agents and editors (below).
- `setup` reports how far a project's FACET setup goes and installs a
  pre-commit hook or a `PATH` link on request.
- `version --json` reports the product, version, build origin and the
  specification and record-schema versions the binary supports.

## MCP and LSP

`facet mcp` serves the registry over MCP on stdio. Of the twelve reference tool
names in §6.2 of the specification it provides five: `feature_find`,
`feature_get`, `anchor_resolve`, `anchor_list` and `registry_validate`. It also
provides `read_facet`, `rag_query` (which needs an index from `facet rag index`),
`source_view_get`, and `glossary_*` tools that read a project's glossary and
change it on a branch, commit there and open a pull request. Check the server's
`tools/list` response when configuring an integration.

`facet lsp` serves hover, go-to-definition, completion and diagnostics for
`@facet:` anchors over LSP on stdio.

## Claude Code and Codex plugin

The Claude Code plugin manifest carries the FACET release version. The manifests
are `.claude-plugin/plugin.json` and `.codex-plugin/plugin.json`; skills are in
`skills/`, agent profiles in `agents/`, hooks in `hooks/hooks.json`. The MCP
server and the hooks run the `facet` binary that `bin/facet` resolves.

In Claude Code, add this repository as a marketplace and install the plugin:

```text
/plugin marketplace add facet-framework/core
/plugin install facet@facet-framework
```

Or load a local checkout for one session:

```sh
claude --plugin-dir /path/to/core
```

Codex installation, the skill names and the host differences are described in
[docs/CODEX.md](docs/CODEX.md).

## Use the registry library

The `registry` import path currently declares Go package `facet`. An explicit
alias makes consumer code read consistently:

```go
package main

import (
    "fmt"
    "log"

    registry "github.com/facet-framework/core/registry"
)

func main() {
    registries, err := registry.OpenAll(".")
    if err != nil {
        log.Fatal(err)
    }
    report, err := registry.LintAll(".", registry.LintOptions{})
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%d registries: %+v\n", len(registries), report)
}
```

`OpenAll` resolves the project's registry layout. `LintAll` checks those registries
and returns their combined report. Treat these APIs as unstable during the release
candidate series. `cli.NewCommand` assembles the same commands used by the binary;
applications inject their version and any optional command integrations there.

## Not in this release

- **Atlas.** This repository has no `ui` command and no browser application. FACET Atlas,
  the local reader, is a separate product with its own release.
- **Wiki.** The wiki commands are not included.
- Hosted deployments.

## Try it on an example

From a source checkout, read the [minimal example](examples/minimal/), compare
its record with the `@facet:` comment in `src/task.go`, then run:

```sh
npx facet-framework@0.1.0-rc.1 --root examples/minimal lint
```

## Source map

- [cmd/facet/](cmd/facet/): command-line entry point.
- [cli/](cli/): command assembly and protocol adapters.
- [registry/](registry/): registry, schema, scanning and validation logic.
- [corpus/](corpus/), [rag/](rag/), [internal/sourceview/](internal/sourceview/), [internal/docsite/](internal/docsite/): repository
  data, retrieval, source views and documentation sites.
- [internal/remote/](internal/remote/), [docs/integrations/](docs/integrations/): the read-only REST and MCP
  integration served by `facet serve`.
- [pkg/productplugin/](pkg/productplugin/), [pkg/filelock/](pkg/filelock/), [release/](release/): product protocol, file
  locking and build identity.
- [schema/](schema/): public schemas.
- [docs/framework/](docs/framework/): the FACET specification.
- [bin/](bin/), [hooks/](hooks/), [scripts/](scripts/), [tools/facet-lint/](tools/facet-lint/): dispatcher, hooks, release
  scripts and the shell lint suite.

## License and contributions

This repository's code, schemas and specification are [Apache-2.0](LICENSE). Third-party
notices are in [NOTICE](NOTICE), [THIRD_PARTY.md](THIRD_PARTY.md),
`THIRD_PARTY.json` and `THIRD_PARTY_LICENSES/`.
See [CONTRIBUTING.md](CONTRIBUTING.md) for changes and [SECURITY.md](SECURITY.md)
for private vulnerability reporting.

Contributions require the [FACET Contributor License Agreement](CLA.md), which
authorizes Pascal Malbranche to relicense contributions, including under
proprietary or commercial terms. Contributors retain their copyright.

## Project governance

FACET publishes curated releases maintained by Pascal Malbranche.
Contributions are welcome under the CLA described in CONTRIBUTING.md; accepted
patches enter the next reviewed release rather than updating public release
branches directly. The CLA permits future commercial or proprietary licensing
of covered contributions. Existing releases retain their published licenses.
No response time or support commitment is promised.
