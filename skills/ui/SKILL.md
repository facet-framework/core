---
name: ui
description: Open FACET Atlas — the browser reader over this project's registry — with 'facet ui'. Use when the user asks to open, show, start, or look at the UI, Atlas, the reader, the browser view, the dashboard, or the visual map of the FACET records, or asks to see the registry, coverage, or anchors as a picture rather than a list.
---

# Open the FACET reader

```
facet ui
```

Starts a local server over this repository and opens the browser at it. The
address is printed on the first line.

In Core release archives and the npm package `facet-framework`, `facet ui`
is the recommended way to get the browser reader. On its first run it asks
before downloading FACET Atlas. Run `facet ui --atlas-version` to check the
versions and local install status without a network request.
Under a Core pin, `facet ui` needs the pin to match the Core inside Atlas. `facet ui --atlas-version` shows both versions.

If you are an agent without a terminal and Atlas is missing, tell the user
the pinned Atlas version, that the download is about 20 MB and licensed
under AGPL-3.0, and that it comes from `github.com/facet-framework/atlas`.
After the user agrees, start `facet ui --yes --no-open` in the background.

## Start it in the background

The command serves until it is stopped, so a foreground run would block the
session. Start it in the background and report the address it printed:

```
facet ui &
```

If the tool you are using has an explicit background mode, use that instead of
`&`. Wait for the address line before answering — if the command exits at once,
it failed, and the reason is in its output.

Never poll the server or fetch pages from it to describe them. The reader is for
the user's eyes; the answers you need come from `facet lint`, `facet registries`
and the records themselves.

## Flags worth knowing

Launcher flags go before Atlas flags. Arguments after `--` pass through to Atlas.

| Flag | When |
|---|---|
| `--yes` | authorize the first download without a prompt |
| `--atlas-version` | show the pinned Atlas and contained Core versions and the local install status |
| `--remove` | remove the pinned Atlas version from the cache |
| `--no-open` | no desktop to open a browser on (SSH, container, CI) — print only |
| `--port N` | the default 7317 is taken by something the user wants to keep |
| `--registry NAME` | a monorepo with several registries; without it, the first declared one |
| `--root PATH` | the repository is not the current directory |
| `--record ID` | the user asked about one record — open straight at it |

A port already in use is not an error: the server falls back to one the system
picks and prints that address instead. Read the address from the output rather
than assuming 7317.

## What the user will see

The reading is streamed while it happens — records, then the file count, then
the search for `@facet:` anchors — so a large repository shows real progress
with the files going past. Then Atlas: the situation in one sentence, a focus
list of what is open, and a register behind `R` for records, promises and tests,
operations, rules, data, dependencies, anchors, ownership and fields. `⌘K`
searches. Reloading the page reads the repository again, so it stays current as
the user edits.

Which record is open is in the address (`…/#tasks/create/quick-add`), so the
address the command prints is the address to pass on. When the user is asking
about a particular feature, start it with `--record` rather than telling them
where to click.

## When it will not start

**"this project has no FACET records under docs/facets"** — the project has not
adopted FACET. Offer `/facet:init`, or ask whether `--root` should point
somewhere else. Do not create records to make the command start.

**`unknown command "ui"`** — check the installed build with
`facet version --json`. Use a Core release with the Atlas launcher or the
FACET Atlas distribution.

**"the Atlas bundle is missing from this build"** — the binary was built without
its embedded reader. Use a published FACET Atlas distribution or follow the
build instructions in the [FACET Atlas repository](https://github.com/facet-framework/atlas).

## Do not

Do not run `pnpm`, `npm` or `next` to start it. FACET Atlas includes the reader,
and Core downloads Atlas on first use. Both run without Node.
