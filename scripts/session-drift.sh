#!/usr/bin/env sh
# @facet: authoring/guardrails/drift-notice
# @facet-role: primary
# Stop hook — at the end of a turn, checks whether any anchored source file
# changed in the working tree without its FACET record changing with it
# (FACET spec 12.1).

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(cat)

# Prefer the binary when one exists; fall through to the shell logic below.
facet_delegate_to_binary "stop" "$INPUT" && exit 0
ROOT=$(facet_project_root "$INPUT")
STOP_ACTIVE=$(printf '%s' "$INPUT" | sed -n 's/.*"stop_hook_active"[[:space:]]*:[[:space:]]*\(true\|false\).*/\1/p')
SESSION=$(facet_json_field '.session_id' "$INPUT")

facet_is_adopted "$ROOT" || exit 0
# Switched off by the person running the session (see facet_drift_notice_off).
facet_drift_notice_off "$ROOT" && exit 0
git -C "$ROOT" rev-parse --git-dir >/dev/null 2>&1 || exit 0

RECORDS_DIR=$(facet_records_dir "$ROOT")
REL_RECORDS=${RECORDS_DIR#"$ROOT"/}

# Everything the working tree has touched, tracked or not.
CHANGED=$(git -C "$ROOT" status --porcelain 2>/dev/null |
  sed 's/^...//; s/^.* -> //' |
  grep -v '^$')
[ -n "$CHANGED" ] || exit 0

# Records that moved in this change set — these are the ones already handled.
TOUCHED_RECORDS=$(printf '%s\n' "$CHANGED" | grep "^$REL_RECORDS/features/" | sed "s|^$REL_RECORDS/features/||; s|\.yaml$||")

FINDINGS=""

while IFS= read -r rel; do
  [ -z "$rel" ] && continue
  case "$rel" in
  "$REL_RECORDS"/*) continue ;;
  esac
  f="$ROOT/$rel"
  [ -f "$f" ] || continue
  facet_is_illustrative "$f" && continue

  ids=$(grep -oE '@facet:[[:space:]]*[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+' "$f" 2>/dev/null |
    sed 's/@facet:[[:space:]]*//' | sort -u)
  [ -n "$ids" ] || continue

  while IFS= read -r id; do
    [ -z "$id" ] && continue
    # Record moved together with the code — nothing to report.
    printf '%s\n' "$TOUCHED_RECORDS" | grep -qxF "$id" && continue
    # Record does not exist at all — check-anchor.sh already covers that case.
    [ -f "$RECORDS_DIR/features/${id}.yaml" ] || continue
    # A planned record is the other order: the promise was written first and the
    # code is arriving to meet it. Source moving while the record stands still
    # is what that looks like from here, and saying "the record did not follow"
    # would be formally true and backwards.

    grep -qE '^lifecycle:[[:space:]]*planned[[:space:]]*$' \
      "$RECORDS_DIR/features/${id}.yaml" 2>/dev/null && continue
    facet_notice_is_new "drift:$id" "$SESSION" || continue
    FINDINGS="$FINDINGS
- $id — changed: $rel"
  done <<IDS
$ids
IDS
done <<FILES
$CHANGED
FILES

[ -n "$FINDINGS" ] || exit 0

# Already delivered records were discarded above, even on a continuation.
# Do not skip new records merely because stop_hook_active is true.
CONTINUATION=""
[ "$STOP_ACTIVE" = true ] && CONTINUATION="These are new notices during the Stop-hook continuation. Previously delivered notices do not block again.

"
REASON="FACET: Review the following source/reference changes before finishing. A changed file is a review signal, not proof of a semantic defect. Check surviving implementation, verification references, Knowledge and lifecycle; repair the record when needed, or explain why the existing record remains correct.

${CONTINUATION}FACET: source implementing these features changed, but their records did not:
$FINDINGS

If any of these changes observable behaviour, the record belongs in the same
change (FACET spec 12.1). If they are behaviour-preserving, explain that review outcome.
Each record is reported here only once per session.

FACET review locations:
- $ROOT

After reviewing, check the current FACET status with 'facet drift --json' in the affected repository or worktree. If the facet binary is unavailable, report that the status check could not run and base the scoped review on the listed files and records; do not claim a clean status. A deduplicated notice is not evidence that drift is resolved. Then send a complete, self-contained final response to the user's original task again: lead with the task result and retain relevant deliverables, links, verification results and remaining limitations. Update any earlier claims invalidated by the review or subsequent edits; do not claim checks passed unless they ran. Include the FACET review outcome briefly after the task result, including unresolved or unrelated changes and any failed status check. Do not finish with only a drift acknowledgement or refer the user to an earlier response. Review unrelated changes without modifying them or accepting a baseline outside the authorized scope."
if command -v jq >/dev/null 2>&1; then
  jq -n --arg reason "$REASON" '{decision: "block", reason: $reason}'
else
  ESCAPED=$(printf '%s' "$REASON" |
    sed 's/\\/\\\\/g; s/"/\\"/g' |
    awk 'NR>1{printf "\\n"} {printf "%s", $0}')
  printf '{"decision":"block","reason":"%s"}\n' "$ESCAPED"
fi
