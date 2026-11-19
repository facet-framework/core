#!/usr/bin/env sh
# @facet: authoring/session/read-context
# @facet-role: related
# PostToolUse Read context uses the binary's anchor parser and registry routing.
# No download is attempted. Without a binary this capability is unavailable;
# existing shell Write/Edit reminders and the Read gate remain independent.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"
INPUT=$(facet_normalize_input "$(cat)")
if facet_delegate_to_binary "post-tool-use" "$INPUT"; then
  exit 0
fi
# Say what is missing once, rather than pretending to have supplied the claims.
FILE=$(facet_json_field '.tool_input.file_path' "$INPUT")
SESSION=$(facet_json_field '.session_id' "$INPUT")
CWD=$(facet_json_field '.cwd' "$INPUT")
case "$FILE" in /*) ;; *) FILE=${CWD:-$(pwd)}/$FILE ;; esac
[ -f "$FILE" ] || exit 0
ROOT=$(git -C "$(dirname -- "$FILE")" rev-parse --show-toplevel 2>/dev/null)
[ -n "$ROOT" ] || ROOT=$(facet_project_root "$INPUT")
facet_is_adopted "$ROOT" || exit 0
facet_is_illustrative "$FILE" && exit 0
if grep -q '@facet:' "$FILE" && facet_notice_is_new "read-context-unavailable" "$SESSION"; then
  facet_emit_context "PostToolUse" "FACET Read context is unavailable without the facet binary. Read the records for this file's anchors before editing; the shell fallback does not resolve roles, lifecycle or named registries. No binary is downloaded by this hook."
fi
exit 0
