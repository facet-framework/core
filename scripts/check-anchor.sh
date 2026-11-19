#!/usr/bin/env sh
# @facet: authoring/guardrails/anchor-reminders
# @facet-role: primary
# PostToolUse hook — when Claude edits a file that carries an @facet: anchor,
# verify the anchor still resolves and remind it that the record travels with
# the change (FACET spec 12.1).

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

check_file() {
  FILE=$1
  [ -n "$FILE" ] || return 0
  [ -f "$FILE" ] || return 0

  facet_is_adopted "$ROOT" || return 0
  facet_is_illustrative "$FILE" && return 0

  RECORDS_DIR=$(facet_records_dir "$ROOT")
  case "$FILE" in
  "$RECORDS_DIR"/*) return 0 ;;
  esac

  TAGS=$(grep -oE '@facet:[[:space:]]*([a-z][a-z0-9-]*:)?[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+' "$FILE" 2>/dev/null |
    sed 's/@facet:[[:space:]]*//' |
    sort -u)
  [ -n "$TAGS" ] || return 0

  REL=${FILE#"$ROOT"/}
  MISSING=""
  KNOWN=""
  QUALIFIED=""

  while IFS= read -r id; do
    [ -z "$id" ] && continue
    case "$id" in
    *:*)
      QUALIFIED="$QUALIFIED $id"
      continue
      ;;
    esac
    if [ -f "$RECORDS_DIR/features/${id}.yaml" ]; then
      KNOWN="$KNOWN $id"
    else
      MISSING="$MISSING
- '$id' has no record at ${RECORDS_DIR#"$ROOT"/}/features/${id}.yaml"
    fi
  done <<LIST
$TAGS
LIST

  QUALIFIED_NOTE=""
  if [ -n "$QUALIFIED" ]; then
    QUALIFIED_NOTE="

Anchors naming a registry (${QUALIFIED# }) are not checked here: this fallback
reads only the default records directory. Install the facet binary, or run
'facet lint', to have them resolved."
  fi

  # A dangling anchor breaks CI, so report it every time.
  if [ -n "$MISSING" ]; then
    printf '%s\n' "$REL carries @facet: anchors that do not resolve:$MISSING

Create the missing record(s) or correct the tag id before committing.$QUALIFIED_NOTE"
    return 0
  fi

  # One marker per feature and file, scoped to the actual repository and the
  # default registry this fallback can inspect. Qualified ids remain distinct
  # even though their records cannot be resolved by this fallback.
  FRESH=0
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    if facet_anchor_notice_is_new "$ROOT" "$RECORDS_DIR" "$id" "$REL" "$SESSION"; then FRESH=1; fi
  done <<LIST
$TAGS
LIST
  [ "$FRESH" = 1 ] || return 0
  if [ -z "$KNOWN" ]; then
    [ -n "$QUALIFIED" ] && printf '%s\n' "$REL carries FACET anchors.$QUALIFIED_NOTE"
    return 0
  fi

  printf '%s\n' "$REL implements FACET feature(s):${KNOWN}.
If this edit changes observable behaviour, update the matching record in this
same change — intent, contract scenarios, and any Knowledge slot it invalidates
(FACET spec 12.1). If the change is behaviour-preserving, no record edit is needed.

This is the shell fallback, which reports less than the binary: it does not
carry the record's intent or contract scenarios, and it cannot resolve anchors
that name a registry. Installing the binary is what closes that gap.$QUALIFIED_NOTE"

  return 0
}

# xargs passes raw NUL-delimited Git paths as argv, which keeps spaces,
# Unicode, quotes and embedded newlines out of shell text parsing.

if [ "${1:-}" = "--source" ]; then
  ROOT=$2
  SESSION=$3
  PATH_ITEM=${5:-}
  [ -n "$PATH_ITEM" ] || exit 0
  if [ -n "$4" ]; then
    SEEN_DIR=$4/$(facet_anchor_component "$PATH_ITEM")
    [ -e "$SEEN_DIR/seen" ] && exit 0
    mkdir -p "$SEEN_DIR" 2>/dev/null && : >"$SEEN_DIR/seen"
    # xargs runs these one at a time, so a plain counter cannot race.
    _count=0
    [ -f "$4/count" ] && read -r _count <"$4/count"
    if [ "$_count" -ge "$FACET_BULK_MAX_FILES" ]; then
      : >"$4/capped"
      exit 0
    fi
    echo $((_count + 1)) >"$4/count"
  fi
  check_file "$ROOT/$PATH_ITEM"
  exit 0
fi

INPUT=$(facet_normalize_input "$(cat)")

facet_binary_path >/dev/null && exit 0
facet_dispatch_patch "$SCRIPT_DIR/check-anchor.sh" "$INPUT" "PostToolUse" && exit 0
ROOT=$(facet_source_root "$INPUT")
FILE=$(facet_json_field '.tool_input.file_path' "$INPUT")
SESSION=$(facet_json_field '.session_id' "$INPUT")
TOOL=$(facet_json_field '.tool_name' "$INPUT")
CWD=$(facet_json_field '.cwd' "$INPUT")

facet_is_adopted "$ROOT" || exit 0
if [ "$TOOL" = "Bash" ]; then
  # The index, working tree and new paths together are the dirty-tree snapshot.
  # Per-event path markers prevent staged+unstaged paths being scanned twice.

  BULK_SEEN=$(mktemp -d 2>/dev/null) || BULK_SEEN=
  trap '[ -z "$BULK_SEEN" ] || rm -rf "$BULK_SEEN"' EXIT HUP INT TERM
  [ -n "$BULK_SEEN" ] || exit 0
  CONTEXT=$({
    git -C "$ROOT" diff --name-only -z 2>/dev/null
    git -C "$ROOT" diff --cached --name-only -z 2>/dev/null
    git -C "$ROOT" ls-files --others --exclude-standard -z 2>/dev/null
  } | (cd "$ROOT" && facet_with_budget xargs -0 grep -lZ -e '@facet:' -- 2>/dev/null) |
    facet_with_budget xargs -0 -n 1 sh "$SCRIPT_DIR/check-anchor.sh" --source "$ROOT" "$SESSION" "$BULK_SEEN")
  if [ -e "$BULK_SEEN/capped" ]; then
    CONTEXT="${CONTEXT:+$CONTEXT

}More than $FACET_BULK_MAX_FILES changed files carry FACET anchors; only the first $FACET_BULK_MAX_FILES were
checked after this command. Run 'facet lint' for the rest."
  fi
else
  [ -n "$FILE" ] || exit 0
  case "$FILE" in /*) ;; *) FILE=${CWD:-$ROOT}/$FILE ;; esac
  CONTEXT=$(check_file "$FILE")
fi
[ -n "$CONTEXT" ] && facet_emit_context "PostToolUse" "$CONTEXT"
exit 0
