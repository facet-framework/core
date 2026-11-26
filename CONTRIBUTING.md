# Contributing to FACET

Open an issue for a reproducible defect or a small, concrete proposal. Include
the release version, expected behavior and a minimal synthetic example. Do not
include credentials, private repository contents or another project's records.
For vulnerabilities, follow [SECURITY.md](SECURITY.md).

Keep pull requests focused. Explain the behavior or responsibility being changed
and run the checks CI runs (`.github/workflows/ci.yml`) that your change
touches, at least:

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .
go build -o dist/facet ./cmd/facet && ./dist/facet lint
```

`gofmt -l .` must print nothing. The shell lint suite
(`FACET_STRICT_DEPS=1 sh tools/facet-lint/run-all.sh`) additionally needs `yq`
4.x and `ajv-cli` 5. Add a regression test for a fixed behavior; avoid tests
that only repeat implementation details. Update documentation and the records
under `docs/facets/` in the same change when behavior they describe changes.
Preserve existing license and copyright notices and disclose copied material with
its upstream source and license.

This repository publishes curated releases. An accepted change may be integrated
with related work before the next tagged release; acceptance does not imply an
immediate package release. Maintainers review the final source and built artifacts
together.

## Build from source

Use the Go version declared in `go.mod`:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-X main.version=0.1.0-rc.5" -o dist/facet ./cmd/facet
./dist/facet --root examples/minimal lint
```

CI runs the Go test suite on Linux amd64, with and without the race detector.
macOS arm64 and Windows amd64 are built and vetted; all six release archives
are cross-compiled and checked to start. The `bin/facet` dispatcher is not
tested on Windows.

## Source map

- [cmd/facet/](cmd/facet/): command-line entry point.
- [cli/](cli/): command assembly and protocol adapters.
- [registry/](registry/): registry, schema, scanning and validation logic.
- [corpus/](corpus/), [rag/](rag/), [internal/sourceview/](internal/sourceview/), [internal/docsite/](internal/docsite/):
  repository data, retrieval, source views and documentation sites.
- [internal/remote/](internal/remote/), [docs/integrations/](docs/integrations/): the read-only REST and MCP
  integration served by `facet serve`.
- [pkg/productplugin/](pkg/productplugin/), [pkg/filelock/](pkg/filelock/), [release/](release/): product protocol, file
  locking and build identity.
- [schema/](schema/), [docs/framework/](docs/framework/): public schemas and the specification.
- [bin/](bin/), [hooks/](hooks/), [skills/](skills/), [agents/](agents/): the plugin's dispatcher, hooks, skills and
  agent profiles.
- [scripts/](scripts/), [tools/facet-lint/](tools/facet-lint/): release scripts and the shell lint suite.
- [`.facetrc.yaml`](.facetrc.yaml), [docs/facets/](docs/facets/): this repository's own FACET registry.

## Rights and contributor agreement

The current project license is **Apache-2.0**; see [LICENSE](LICENSE).
Contribute only work you have the right to submit. Preserve third-party notices
and identify third-party material and its license separately.

Before merge, each contributor must explicitly sign the exact version and
SHA-256 hash of the [FACET Contributor License Agreement](CLA.md), and Pascal
Malbranche or an authorized representative must accept it. Request private
signing instructions at <hello@facet-framework.com>. Do not publish signatures,
legal identities, contact details or employer authorization evidence in this
repository. A PR
checkbox, commit signature or Signed-off-by line does not replace this agreement.
If you submitted your first pull request before signing, include its repository,
pull request number and exact head commit in the signed record of Earlier
Contributions. Both parties must explicitly accept that coverage before merge.

You retain your copyright. The CLA grants Pascal Malbranche broad transferable
and sublicensable rights, including permission to relicense your contribution
under any terms, including proprietary or commercial terms. Future releases may
use different terms, including proprietary terms, for contributions covered by
the CLA. This does not change the license of an existing release.
Open-source rights already granted for earlier releases remain in force under
their existing terms. Third-party material keeps its own terms; the CLA grants
no rights you do not own or have authority to license. Obtain any necessary
employer or other rights holder authorization before submission.

## Agent-assisted contributions

A human contributor or authorized organization must hold the necessary rights
and sign the CLA. An automated agent cannot sign on your behalf or supply
employer authorization. Review generated changes, identify their sources and
licenses, and take responsibility for the submitted patch.
