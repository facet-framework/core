# FACET in Codex

FACET's Codex plugin uses the same authoring skill, guide, schemas, role
profiles and registry implementation as its Claude Code plugin. Records have
the same format and verification rules on both hosts.

The plugin shares the FACET release version, starting at 0.1.0-rc.2. Use a
Codex CLI with native plugin and hook support. Host compatibility for this
Core-versioned plugin has not yet been qualified.

## Install

Use a Codex CLI with native plugin and hook support. First put a native FACET
Core 0.x CLI on `PATH`, then set `FACET_BINARY` to that native executable as
described in [Binary and fallback setup](#binary-and-fallback-setup). Codex
uses this CLI for the registry MCP server; installing the plugin alone does not
supply it.

From a terminal, add the FACET marketplace and install its plugin:

```bash
codex plugin marketplace add facet-framework/core
codex plugin add facet@facet-local
```

For a local checkout of this release, use its absolute path instead:

```bash
codex plugin marketplace add /absolute/path/to/core
codex plugin add facet@facet-local
```

The Codex marketplace name is `facet-local`, including when fetched from
GitHub. Claude's marketplace is `facet-framework`. To fetch a specific Git
ref, use `codex plugin marketplace add facet-framework/core --ref <ref>`.
Use a ref containing the Codex manifests.

Start a new Codex session in the project you want to document. Open `/hooks`
and review and trust FACET's hook definitions. Codex skips untrusted plugin
hooks and requests another review when their definitions change.
[OpenAI's hook documentation](https://learn.chatgpt.com/docs/hooks) describes
this trust flow.

Invoke `$facet:init` for a new project. For a project with existing FACET
records, invoke `$facet:lint` first and use `$facet:migrate` when a schema
migration is needed.

Install the whole plugin package. Copying a `SKILL.md` file alone loses the
relative guide, schema, profile and script paths it uses.

## Invoke skills

Open `/skills` to select an installed skill, or mention its name directly in a
prompt. For example:

```text
$facet:authoring Document the behavior changed by this patch.
$facet:lint Check the records and resolve the reported findings.
$facet:record-writer Write the record for this feature.
```

Codex exposes these actions as skills. `/skills` is the native slash picker;
FACET does not register separate `/facet:...` slash commands in Codex. Claude
Code continues to use its `/facet:...` commands. When shared instructions name
a Claude command, invoke the corresponding Codex skill from the table below.
See [OpenAI's skill documentation](https://learn.chatgpt.com/docs/build-skills)
for skill discovery and distribution.

| Action | Codex invocation | Claude invocation |
|--------|------------------|-------------------|
| Adopt FACET | `$facet:init` | `/facet:init` |
| Write records | `$facet:authoring` | `/facet:authoring` |
| Place source anchors | `$facet:anchoring` | `/facet:anchoring` |
| Validate records | `$facet:lint` | `/facet:lint` |
| Migrate schemas | `$facet:migrate` | `/facet:migrate` |
| Configure project registries | `$facet:registries` | `/facet:registries` |
| Configure variants | `$facet:variants` | `/facet:variants` |
| Maintain taxonomy | `$facet:taxonomy` | `/facet:taxonomy` |
| Generate user documentation | `$facet:knowledge` | `/facet:knowledge` |

Two additional Codex skills load the canonical profiles from `agents/`:
`$facet:record-writer` and `$facet:drift-auditor`. Loading one gives the
current agent the role's instructions. It does not register or start a
subagent, choose a model or apply Claude's `tools` declarations. Codex's own
sandbox and tool policy remain in force. Delegate only when the user or
project instructions allow it.

`$facet:statusline` is also discoverable, but its setup configures Claude's
status line. FACET does not install a Codex status-line integration.

## Hooks and registry access

The `.codex-plugin/plugin.json` manifest points at the shared skills, hooks
and MCP configuration. The native Codex manifest is required for the checked
CLI; the package keeps the Claude manifest as well.

| Event | FACET behavior in Codex |
|-------|-------------------------|
| SessionStart | Supplies registry context and working rules; restores context after compaction |
| PostToolUse | Checks records and anchors changed through shell or patch tools |
| PreToolUse | Supplies anchored source context before patches and runs lint before recognized Git commits |
| PreCompact | Carries FACET working rules forward |
| Stop | Reports outstanding drift review |

Codex shell operations are normalized to Bash hook input. `apply_patch`
changes are normalized to edit input. Before a patch, FACET supplies the
relevant record context for anchored source files. This does not apply the
record-size gate to patch operations or arbitrary shell reads. That gate
remains attached to explicit Read events, as used by Claude. These hooks use
the same scripts and binary as Claude, with host input adapted before checks.
Their availability still depends on Codex enabling hooks and trusting the
current definitions.

The shared hook matchers explicitly include `exec_command` and `apply_patch`
alongside Claude tool names. Shared skills resolve bundled guides relative to
their installed skill directory, independently of host environment variables.

Codex's `.mcp-codex.json` starts the native `facet` executable from `PATH`
with `mcp --root .`, preserving the session working directory, including a
Git worktree. The checked Codex runtime does not expand plugin-root macros
in MCP commands, so its server uses this separate configuration. Claude
retains `.mcp.json` and `scripts/mcp-launch.sh`, which resolve the plugin
resources and supplied project directory automatically.

The registry server supports record lookup and validation. FACET's LSP
registration in `.lsp.json` is for Claude Code; this package does not register
a native Codex LSP. Use the lint skill, registry MCP tools or FACET CLI for
record and anchor checks in Codex.

## Binary and fallback setup

A FACET 1.x binary is a previous product line: the dispatcher passes it over,
and it is not a supported CLI for this plugin. Run the plugin's `bin/facet`
dispatcher once. It downloads the Core release archive
`facet_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) from the `v<version>`
release of `facet-framework/core`, verifies it against that release's
`checksums.txt` before unpacking, and installs it under its cache. Then point
Codex at the binary it installed:

```bash
bin/facet version
base="${CLAUDE_PLUGIN_DATA:-${XDG_CACHE_HOME:-$HOME/.cache}/facet}"
"$base/dist/core/selected/facet" version --json   # must name "product":"core"
export FACET_BINARY="$base/dist/core/selected/facet"
export PATH="$(dirname "$FACET_BINARY"):$PATH"
```

Run `facet ui` to start the browser reader. Core asks before downloading the
separate FACET Atlas distribution on first use.

Start Codex from the target project with this environment. `PATH` provides
the native CLI to MCP, while `FACET_BINARY` selects it for the hooks. Preserve
those settings in your normal shell setup if you want them in later sessions.

The Claude plugin retains its versioned `bin/facet` dispatcher, which resolves
a cached binary or downloads and verifies a release. Codex's native MCP
configuration does not use that dispatcher or bootstrap a binary
automatically.

Hooks do not download or build binaries. Without `FACET_BINARY` or an existing
plugin-cache binary, they use the shell fallback where supported. The patch
fallback requires `jq` for lossless patch decoding, alongside the lint suite's
`yq` and `ajv-cli` dependencies. Missing checks are reported as unavailable;
they are not successful validation. The fallback does not replace the native
CLI required by the Codex MCP configuration.

After installation, confirm the FACET entries appear in `/skills`, review
FACET's entries in `/hooks`, and invoke `$facet:lint` in the target project.
A discovered skill or installed plugin alone does not establish that trusted
hooks and registry MCP access are running in that project.

## Host acceptance

Use a fresh Codex session against a disposable adopted project: confirm
skills, a registry MCP lookup, trusted SessionStart context, feedback after an
invalid record patch, context before changing anchored source, denial of a
commit with a new staged unresolved anchor, and restored working rules after
compaction. Record the CLI and plugin versions and each observable result.
LSP and the Claude status line remain host differences.
