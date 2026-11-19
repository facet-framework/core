#!/usr/bin/env sh
# @facet: authoring/guardrails/large-record-read
# @facet-role: related
# PreToolUse hook on Read — hands the event to the facet binary, which sends an
# agent reading a large record whole to read_facet instead.

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(facet_normalize_input "$(cat)")
facet_delegate_to_binary "pre-tool-use" "$INPUT"
exit 0
