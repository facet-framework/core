# Third-party licenses

This file does not list dependencies by hand. The release carries two generated
files at the repository root and in every release archive:

- `THIRD_PARTY.json`, an inventory of the exact versions of the Go modules
  compiled into the `facet` command on any of the six release platforms (Linux,
  macOS and Windows on amd64 and arm64) and of the Go runtime and standard
  library. Each entry names its ecosystem, package, version and role, and the
  files under `THIRD_PARTY_LICENSES/` with their SHA-256 hashes.
- `THIRD_PARTY_LICENSES/`, the license, copyright and NOTICE texts of those
  components, copied verbatim.

Both are generated before the source is published, from the compiled package
closure of the command on every release platform, and committed here; a
compiled component without a verbatim license or notice file stops that
collection. The release build packages the committed files as they are.

CI and the release build run `scripts/release-third-party-check.sh`. It fails
when a Go module the command compiles on any release platform is missing from
`THIRD_PARTY.json` or listed at another version, when the inventory lists a
module no platform compiles, when the Go toolchain building the release is not
the one the inventory was collected with, or when a listed file is missing or
has another hash. CI also checks that every file NOTICE refers to is present.
A dependency or Go version change therefore needs a new inventory before it
can be released.

A top-level project license does not replace these third-party terms, and the
inventory does not change any component's license or grant additional rights.

Keep the required texts and notices when redistributing source, binaries or
other artifacts. Copied upstream source is also attributed in this project's
[NOTICE](NOTICE).
