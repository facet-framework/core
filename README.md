<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/facet-mark-dark.svg">
    <img src="docs/assets/facet-mark.svg" alt="FACET" width="72">
  </picture>
</p>

<h1 align="center">FACET</h1>

<p align="center">
  Keep your product's features, code and tests from drifting apart.
</p>

<p align="center">
  <a href="https://github.com/facet-framework/core/actions/workflows/ci.yml"><img src="https://github.com/facet-framework/core/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

<p align="center">
  <a href="https://facet-framework.com">Website</a> ·
  <a href="docs/framework/FACET.md">Specification</a> ·
  <a href="docs/AUTHORING.md">Authoring guide</a> ·
  <a href="CHANGELOG.md">Changelog</a>
</p>

---

FACET describes each named capability of your product in a YAML record and
connects it to the source that implements it with `@facet:` comments. The
`facet` command checks both directions: every active record has code, and
every comment has a record.

Version **0.1.0-rc.2**, specification edition **0.1.0**.
This is a release candidate; commands, flags, Go APIs, MCP tools and schemas
may still change before 0.1.0.

## Quick start

In the root of your project:

```sh
npx facet-framework@0.1.0-rc.2 init
```

`init` writes the record layout, schemas, specification, lint tools and a CI
workflow. Existing files are kept unless you pass `--force`.

Pick one capability your project already has and describe it in
`docs/facets/features/<domain>/<capability>/<feature>.yaml`. Mark the code
that implements it with a `@facet: <domain>/<capability>/<feature>` comment. The
[minimal example](examples/minimal/) shows a complete record and anchor.

Then check it:

```sh
npx facet-framework@0.1.0-rc.2 lint
```

## Install

**npm** (Node.js 18+, Linux, macOS or Windows on x64 or arm64). Release
candidates use the npm tag `next`, so name the version:

```sh
npm install --save-dev facet-framework@0.1.0-rc.2
npx facet lint
```

**Release archives.** Each
[release](https://github.com/facet-framework/core/releases) carries one archive
per platform (`facet_<version>_<os>_<arch>`) and a `checksums.txt`:

```sh
version=0.1.0-rc.2
archive="facet_${version}_linux_amd64.tar.gz"
base="https://github.com/facet-framework/core/releases/download/v${version}"
curl -fsSLO "$base/$archive"
curl -fsSLO "$base/checksums.txt"
grep "  $archive\$" checksums.txt | sha256sum --check   # macOS: shasum -a 256 --check
tar -xzf "$archive" facet
./facet version
```

## Commands

| Command | Purpose |
| --- | --- |
| `facet init` | Set up FACET in a project. |
| `facet lint` | Check records against their schema and the source they anchor. |
| `facet drift` | Report anchored source that changed without its record. |
| `facet test` | Run the test commands a project declares. |
| `facet docs site` | Build a documentation site from the registry. |
| `facet mcp` | Serve the registry to agents over MCP (stdio). |
| `facet lsp` | Hover, go-to-definition and diagnostics for `@facet:` anchors. |
| `facet setup` | Report and complete a project's FACET setup. |

`facet --help` lists every command.

## Claude Code and Codex

The plugin adds skills for writing records and placing anchors, hooks that
check records as you edit, and the MCP and LSP servers.

```text
/plugin marketplace add facet-framework/core
/plugin install facet@facet-framework
```

For Codex, see [docs/CODEX.md](docs/CODEX.md).

## Documentation

- [Specification](docs/framework/FACET.md) and [JSON schemas](schema/)
- [Authoring guide](docs/AUTHORING.md)
- [Anchor guide](docs/ANCHOR-GUIDE.md)
- [Registries guide](docs/REGISTRIES-GUIDE.md)
- [Examples](examples/)
- Go library: [`registry`](registry/) (unstable during the release candidates)

## License

[Apache-2.0](LICENSE). Third-party notices are in [NOTICE](NOTICE) and
[THIRD_PARTY.md](THIRD_PARTY.md). See [CONTRIBUTING.md](CONTRIBUTING.md) to
contribute and [SECURITY.md](SECURITY.md) to report a vulnerability.
