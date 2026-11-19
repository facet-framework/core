#!/usr/bin/env sh
# @facet: authoring/guardrails/write-validation
# @facet-role: primary
# PostToolUse hook — validates a single FACET record the moment it is written,
# so schema errors surface while Claude still has the context to fix them
# instead of turning up in CI on the pull request.

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(facet_normalize_input "$(cat)")

# Prefer the binary when one exists; fall through to the shell logic below.
facet_delegate_to_binary "post-tool-use" "$INPUT" && exit 0
facet_dispatch_patch "$SCRIPT_DIR/validate-record.sh" "$INPUT" "PostToolUse" && exit 0
ROOT=$(facet_source_root "$INPUT")
FILE=$(facet_json_field '.tool_input.file_path' "$INPUT")
CWD=$(facet_json_field '.cwd' "$INPUT")
SESSION=$(facet_json_field '.session_id' "$INPUT")
case "$FILE" in /* | "") ;; *) FILE=${CWD:-$ROOT}/$FILE ;; esac

[ -n "$FILE" ] || exit 0
case "$FILE" in
*.yaml | *.yml) ;;
*) exit 0 ;;
esac
[ -f "$FILE" ] || exit 0

facet_is_adopted "$ROOT" || exit 0

RECORDS_DIR=$(facet_records_dir "$ROOT")
case "$FILE" in
"$RECORDS_DIR"/*) ;;
*) exit 0 ;;
esac

REL=${FILE#"$ROOT"/}
case "${FILE#"$RECORDS_DIR"/}" in
features/*.yaml)
  RESET_ID=${FILE#"$RECORDS_DIR"/features/}
  facet_clear_anchor_notices "$ROOT" "$RECORDS_DIR" "${RESET_ID%.yaml}" "$SESSION"
  ;;
esac
PROBLEMS=""

# Check 1 — the record's id must match its path. This needs no external tools
# and catches the single most common authoring mistake: a copied record whose
# id still points at the file it was copied from.
case "${FILE#"$RECORDS_DIR"/}" in
features/*)
  DECLARED=$(grep '^id:[[:space:]]' "$FILE" 2>/dev/null |
    head -n 1 | sed 's/^id:[[:space:]]*//' | tr -d '[:space:]"')
  EXPECTED=${FILE#"$RECORDS_DIR"/features/}
  EXPECTED=${EXPECTED%.yaml}
  if [ -n "$DECLARED" ] && [ "$DECLARED" != "$EXPECTED" ]; then
    PROBLEMS="$PROBLEMS
- id mismatch: the record declares 'id: $DECLARED' but its path implies
  '$EXPECTED'. Rename the file to match the id, or correct the id."
  fi
  ;;
esac

# Check 2 — full schema validation, when the toolchain is available.
LINT_DIR=$(facet_lint_dir "$ROOT")
if command -v yq >/dev/null 2>&1 && command -v ajv >/dev/null 2>&1; then
  SCHEMA_OUT=$(sh "$LINT_DIR/validate-schemas.sh" "$ROOT" "$FILE" 2>&1)
  if [ $? -ne 0 ]; then
    PROBLEMS="$PROBLEMS
- schema validation failed:
$(printf '%s' "$SCHEMA_OUT" | sed 's/^/  /')"
  fi
fi

[ -n "$PROBLEMS" ] || exit 0

facet_emit_context "PostToolUse" "FACET record $REL has problems that need fixing now:
$PROBLEMS"
