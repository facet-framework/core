# Changelog

All notable changes to this repository are listed here, newest first. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions
follow [Semantic Versioning](https://semver.org/). Release candidates may still
change any public API, flag or schema.

## 0.1.0-rc.1

First public release candidate of FACET. Commands, flags, Go APIs, MCP
tools and schemas may still change before 0.1.0.

### Added
- The `facet` command: `init`, `lint`, `drift`, `test`, `docs`, `setup`,
  `mcp`, `lsp`, `version` and the further commands listed by `facet --help`.
- The FACET specification, edition 0.1.0, in `docs/framework/FACET.md`, and
  the JSON schemas in `schema/`.
- An MCP server providing five of the twelve reference tool names in §6.2 of
  the specification (`feature_find`, `feature_get`, `anchor_resolve`,
  `anchor_list`, `registry_validate`) plus record, retrieval, source-view and
  glossary tools.
- A language server for `@facet:` anchors.
- The Go registry library (`registry`) and command assembly (`cli`).
- The Claude Code and Codex plugin, versioned with FACET releases: skills, the `drift-auditor`
  and `record-writer` agent profiles, hooks, MCP and LSP configuration, and the
  `bin/facet` dispatcher, which downloads a release archive and checks it
  against the release's `checksums.txt` before installing it.
- Release archives for Linux, macOS and Windows on amd64 and arm64, with
  SHA-256 checksums and the third-party notices in each archive.
- The npm packages `facet-framework` and `@facet-tools/core` with one platform
  package per binary, published under the npm tag `next`.
- Examples: a minimal Go registry and test-runner projects for Jest, Vitest
  and PHPUnit.
- This repository's FACET registry, linted in CI with the command built from this
  tree.
- Git discovery honours `GIT_CEILING_DIRECTORIES`.
- CONTRIBUTING.md, SECURITY.md, SUPPORT.md and CODE_OF_CONDUCT.md (Contributor
  Covenant 2.1), naming `hello@facet-framework.com` for private reports, CLA
  signing and everything that is not an issue.
- Issue forms with contact links, Dependabot configuration, a CI badge and a
  FACET lint workflow that pins its checkout action by commit.

### Known limitations
- The Go test suite runs on Linux amd64 only. macOS arm64 and Windows amd64
  are built and vetted; all six archives are built and checked to start, but
  the test suite does not run on macOS or Windows.
- The `bin/facet` dispatcher is a POSIX shell script and is not tested on
  Windows.
- Atlas, the local reader, is not included.
- The wiki commands are not included.
