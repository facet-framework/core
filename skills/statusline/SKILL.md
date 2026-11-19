---
name: statusline
description: Show the FACET registry state in the Claude Code status line — record counts, active features, and how many still need owner-supplied Knowledge answers. Use when the user asks to display, show, or add FACET to their status line, or wants the registry visible while they work.
---

# Put the FACET registry in the status line

The plugin ships `facet-statusline`, which prints one segment:

```
FACET 42 records · 38 active · 6 need knowledge
```

It reads only record files, never the source tree, so it stays instant. Outside
a FACET project it prints nothing.

## Wiring it up

A plugin cannot set the main status line itself, so this goes in the project's
`.claude/settings.json`. **Ask before writing it** — the status line is the
user's, and there is usually already something there.

Check for an existing `statusLine` entry first.

**Nothing configured yet:**

```json
{
  "statusLine": {
    "type": "command",
    "command": "facet-statusline"
  }
}
```

**Something already configured** — do not replace it. Compose instead, so the
FACET segment is appended to what they already have:

```json
{
  "statusLine": {
    "type": "command",
    "command": "sh -c 'input=$(cat); printf \"%s\" \"$input\" | <their existing command>; printf \" | \"; printf \"%s\" \"$input\" | facet-statusline'"
  }
}
```

Read their existing command and adapt this shape to it rather than pasting it
blind — the input JSON has to reach both commands, and a status line that
silently prints nothing is worse than one that was never changed.

## Caveat worth stating

`facet-statusline` comes from the plugin, so the status line breaks if the
plugin is disabled — it prints nothing and the segment disappears. That is
harmless, but say it out loud, because the setting lives in the project and may
be committed and shared with people who do not have the plugin installed.

If the project wants it committed and shared, vendor the script into the
repository instead and point the setting at that path.

## Verify

The status line refreshes on the next interaction, not immediately. Run
`facet-statusline < /dev/null` to confirm the script produces output, and tell
the user the bar updates on their next message.
