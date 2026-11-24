#!/usr/bin/env sh
# @facet: authoring/guardrails/commit-gate
# @facet-role: primary
# PreToolUse hook — the binary compares staged errors against HEAD.
# Without the binary, the fallback retains its strict whole-working-tree lint;
# it cannot establish a revision baseline or distinguish existing errors.

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(facet_normalize_input "$(cat)")

# Prefer the binary when one exists; fall through to the shell logic below.
facet_delegate_to_binary "pre-tool-use" "$INPUT" && exit 0
facet_dispatch_patch "$SCRIPT_DIR/read-context.sh" "$INPUT" "PreToolUse" && exit 0

[ "$FACET_SKIP_COMMIT_GATE" = "1" ] && exit 0

COMMAND=$(facet_json_field '.tool_input.command' "$INPUT")
[ -n "$COMMAND" ] || COMMAND=$(facet_json_field '.tool_input.cmd' "$INPUT")
[ -n "$COMMAND" ] || exit 0

# Only interested in commits. Anything else passes through untouched.
printf '%s' "$COMMAND" | grep -qE '(^|[;&|[:space:]])git[[:space:]]+([-a-zA-Z0-9=_/.]+[[:space:]]+)*commit([[:space:]]|$)' || exit 0

ROOT=$(facet_project_root "$INPUT")

CD_TARGET=$(printf '%s' "$COMMAND" | sed -n "s/^[[:space:]]*cd[[:space:]]\{1,\}'\{0,1\}\([^';&|]*[^';&| ]\)'\{0,1\}[[:space:]]*\(&&\|;\).*/\1/p")
if [ -n "$CD_TARGET" ]; then
  case "$CD_TARGET" in
  /*) ;;
  *) CD_TARGET=$(facet_json_field '.cwd' "$INPUT")/$CD_TARGET ;;
  esac
  ROOT=$(git -C "$CD_TARGET" rev-parse --show-toplevel 2>/dev/null) || exit 0
fi

# A gate that cannot run must say so rather than pass quietly: a silent exit
# here is indistinguishable from a clean registry, which is the one thing this
# hook exists to stop anyone from believing without evidence.
if facet_declares_registries "$ROOT"; then
  facet_emit_context "PreToolUse" "FACET: this project declares several registries in .facetrc.yaml, which this
shell fallback cannot read, so the commit gate did NOT check them. The commit
is not blocked and is also not verified. Run 'facet lint' to check it."
  exit 0
fi

facet_is_adopted "$ROOT" || exit 0

# The drift notice's switch follows the same rule as the gate's own: a commit
# that sets it while HEAD does not is refused, before the gate's switch below
# can let it through.
if facet_drift_notice_switch "$ROOT" ":" && ! facet_drift_notice_switch "$ROOT" "HEAD:"; then
  REASON="FACET: this commit sets hooks.drift_notice: off in .facetrc.yaml. Switching the
drift notice off is a decision for the person running the session, not for the agent.
It can be committed with FACET_SKIP_COMMIT_GATE=1 set in the environment Claude
Code was started with, or from a terminal."
  if command -v jq >/dev/null 2>&1; then
    jq -n --arg r "$REASON" \
      '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}'
  else
    ESC=$(printf '%s' "$REASON" |
      sed 's/\\/\\\\/g; s/"/\\"/g' |
      awk 'NR>1{printf "\\n"} {printf "%s", $0}')
    printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$ESC"
  fi
  exit 0
fi

if ! printf '%s' "$COMMAND" | grep -qE '(GIT_DIR=|GIT_WORK_TREE=|--git-dir|--work-tree|pushd|(^|[[:space:]])(sh|bash)[[:space:]]+-c|(^|[^[:alnum:]_])eval([[:space:]]|$))'; then
  facet_commit_gate_off "$ROOT" && exit 0
fi

if facet_commit_gate_off "$ROOT" ":" && ! facet_commit_gate_off "$ROOT" "HEAD:"; then
  REASON="FACET: this commit sets hooks.commit_gate: off in .facetrc.yaml. Switching the
gate off is a decision for the person running the session, not for the agent.
It can be committed with FACET_SKIP_COMMIT_GATE=1 set in the environment Claude
Code was started with, or from a terminal."
  if command -v jq >/dev/null 2>&1; then
    jq -n --arg r "$REASON" \
      '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}'
  else
    ESC=$(printf '%s' "$REASON" |
      sed 's/\\/\\\\/g; s/"/\\"/g' |
      awk 'NR>1{printf "\\n"} {printf "%s", $0}')
    printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$ESC"
  fi
  exit 0
fi

LINT_DIR=$(facet_lint_dir "$ROOT")
OUTPUT=$(sh "$LINT_DIR/run-all.sh" "$ROOT" 2>&1)
STATUS=$?

[ "$STATUS" -eq 0 ] && exit 0

# Trim to the lines that state what is wrong; the full log is noise here.
DETAIL=$(printf '%s' "$OUTPUT" | grep -E '^(  (ERROR|WARNING)|FAIL)' | head -n 25)
[ -n "$DETAIL" ] || DETAIL=$(printf '%s' "$OUTPUT" | tail -n 15)

REASON="FACET shell fallback lint fails over the entire working tree.
The comparison of staged errors against HEAD requires the facet binary; this
fallback still blocks existing errors:

$DETAIL

Fix the records or the anchors, then commit again. Run 'facet-lint' for the full
report.

To commit anyway, FACET_SKIP_COMMIT_GATE=1 must be set in the
environment Claude Code was started with. Prefixing it to the git command does
not reach this gate. An agent cannot set it: ask the person running the session."

if command -v jq >/dev/null 2>&1; then
  jq -n --arg r "$REASON" \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}'
else
  ESC=$(printf '%s' "$REASON" |
    sed 's/\\/\\\\/g; s/"/\\"/g' |
    awk 'NR>1{printf "\\n"} {printf "%s", $0}')
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$ESC"
fi
exit 0
