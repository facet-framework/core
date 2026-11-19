#!/usr/bin/env sh
# @facet: authoring/session/hook-dispatch
# @facet-role: primary
# Shared helpers for the FACET plugin's hook scripts. Sourced, never executed.

FACET_PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"

# Reuse the linter's path-resolution rules so hooks and CI agree on where
# records live.
. "$FACET_PLUGIN_ROOT/tools/facet-lint/lib.sh"

facet_is_illustrative() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
  *.md | *.mdx | *.markdown | *.rst | *.adoc | *.asciidoc | *.txt | *.rtf | *.org)
    return 0
    ;;
  esac
  # Literal split so this line does not match itself.
  head -n 10 "$1" 2>/dev/null | grep -q '@facet-ignore''-file'
}

facet_binary_path() {
  [ "$FACET_NO_BINARY" = "1" ] && return 1
  if [ -n "$FACET_BINARY" ]; then
    # Set but not runnable is a mistake, which bin/facet refuses outright; a
    # hook does not guess past it either and leaves the event to the shell.
    [ -f "$FACET_BINARY" ] && [ -x "$FACET_BINARY" ] || return 1
    printf '%s\n' "$FACET_BINARY"
    return 0
  fi

  _base="${CLAUDE_PLUGIN_DATA:-${XDG_CACHE_HOME:-$HOME/.cache}/facet}"
  if [ -x "$_base/dist/core/selected/facet" ]; then
    printf '%s\n' "$_base/dist/core/selected/facet"
    return 0
  fi
  return 1
}

# Hand this event to the facet binary when one is already available, and return
# otherwise so the shell implementation below handles it.

facet_delegate_to_binary() {
  _event="$1"
  _payload="$2"
  _bin=$(facet_binary_path) || return 1
  printf '%s' "$_payload" | "$_bin" hook "$_event" 2>/dev/null
  return 0
}

# Codex shell calls are equivalent to Bash for the existing commit gate.
facet_normalize_input() {
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$1" | jq -c 'if .tool_name == "exec_command" then .tool_name = "Bash" else . end | if .tool_name == "Bash" then .tool_input.command = (.tool_input.command // .tool_input.cmd // "") else . end' 2>/dev/null
  else
    printf '%s' "$1"
  fi
}

# Reuse each script's existing per-file handling. No patch text is executed.
# jq is needed to decode escaped patch lines and encode filenames losslessly.
facet_dispatch_patch() {
  _patch_script=$1
  _patch_input=$2
  _patch_event=$3
  [ "$(facet_json_field '.tool_name' "$_patch_input")" = apply_patch ] || return 1
  if ! command -v jq >/dev/null 2>&1; then
    _patch_root=$(facet_project_root "$_patch_input")
    facet_is_adopted "$_patch_root" || return 0
    facet_emit_context "$_patch_event" "FACET apply_patch checks require the facet binary or jq. This patch was not checked by the shell fallback. Run facet lint before committing."
    return 0
  fi
  _patch_items=$(printf '%s' "$_patch_input" | jq -c '
    . as $input | (.tool_input.command // "") | split("\n") |
    if (.[0] == "*** Begin Patch" and (.[-1] == "*** End Patch" or (.[-1] == "" and .[-2] == "*** End Patch"))) then
      map(select(test("^\\*\\*\\* (Add File|Update File|Delete File|Move to): ")) | sub("^\\*\\*\\* (Add File|Update File|Delete File|Move to): "; "")) | unique | .[] | select(length > 0) |
      . as $path | $input | .tool_name = "Edit" | .tool_input = {file_path: $path}
    else empty end' 2>/dev/null)
  [ -n "$_patch_items" ] || return 0
  _patch_outputs=$(printf '%s\n' "$_patch_items" | while IFS= read -r _patch_item; do
    _patch_path=$(facet_json_field '.tool_input.file_path' "$_patch_item")
    _patch_cwd=$(facet_json_field '.cwd' "$_patch_item")
    # The patterns carry their opening parenthesis: inside $(...) bash 3.2
    # (macOS) reads a bare "/*)" as the end of the command substitution.
    case "$_patch_path" in /*) ;; *) _patch_path=${_patch_cwd:-$(pwd)}/$_patch_path ;; esac
    _patch_dir=$(dirname -- "$_patch_path")
    while [ ! -d "$_patch_dir" ] && [ "$_patch_dir" != / ]; do _patch_dir=$(dirname -- "$_patch_dir"); done
    _patch_top=$(git -C "$_patch_dir" rev-parse --show-toplevel 2>/dev/null) || continue
    facet_is_adopted "$_patch_top" || continue
    printf '%s' "$_patch_item" | sh "$_patch_script"
  done)
  [ -n "$_patch_outputs" ] || return 0
  printf '%s' "$_patch_outputs" | jq -sc --arg event "$_patch_event" '
    map(.hookSpecificOutput) as $outputs |
    {hookSpecificOutput: ({hookEventName: $event} +
      ([$outputs[].additionalContext // empty] | if length > 0 then {additionalContext: join("\n\n")} else {} end) +
      ([$outputs[] | select(.permissionDecision == "deny") | .permissionDecisionReason] | if length > 0 then {permissionDecision: "deny", permissionDecisionReason: join("\n\n")} else {} end))}' 2>/dev/null
  return 0
}

# Upper bound on the anchored files a Bash event inspects, and the seconds its
# work may take. A Bash event fires after every command an agent runs, so its
# cost has to stay flat however dirty the tree is.
FACET_BULK_MAX_FILES=${FACET_BULK_MAX_FILES:-40}
FACET_BULK_SECONDS=${FACET_BULK_SECONDS:-8}

facet_with_budget() {
  if command -v timeout >/dev/null 2>&1; then
    timeout -k 1 "$FACET_BULK_SECONDS" "$@"
  else
    "$@"
  fi
}

# Extract a string field from hook stdin JSON. Uses jq when available and falls
# back to a flat-key scan, so the plugin still works on machines without jq.
facet_json_field() {
  _filter="$1"
  _json="$2"
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$_json" | jq -r "$_filter // empty" 2>/dev/null
    return 0
  fi
  _key=${_filter##*.}
  printf '%s' "$_json" |
    tr '{,' '\n\n' |
    grep -o "\"$_key\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" |
    head -n 1 |
    sed 's/^.*:[[:space:]]*"//; s/"$//'
}

facet_project_root() {
  if [ -n "$CLAUDE_PROJECT_DIR" ]; then
    printf '%s\n' "$CLAUDE_PROJECT_DIR"
    return 0
  fi
  _cwd=$(facet_json_field '.cwd' "$1")
  if [ -n "$_cwd" ]; then
    printf '%s\n' "$_cwd"
    return 0
  fi
  git rev-parse --show-toplevel 2>/dev/null || pwd
}

# True when the committed .facetrc.yaml at HEAD says
# hooks:
# commit_gate: off
# Mirrors commitGateOffAt in the CLI package (hook_commitgate.go).

facet_commit_gate_off() {
  _gate_spec="${2:-HEAD:}"
  _gate_value=$(git -C "$1" show "${_gate_spec}$FACET_CONFIG_NAME" 2>/dev/null | awk '
    /^[^[:space:]#]/ { in_hooks = ($0 ~ /^hooks:[[:space:]]*(#.*)?$/); next }
    in_hooks && /^  commit_gate:/ {
      v = $0
      sub(/^  commit_gate:[[:space:]]*/, "", v)
      sub(/[[:space:]]*#.*$/, "", v)
      print v
      exit
    }' | tr -d "\"'")
  [ "$_gate_value" = "off" ]
}

# True only when the project actually adopted FACET. Every hook exits silently
# when this is false, so installing the plugin is inert in unrelated projects.
facet_is_adopted() {
  [ -d "$(facet_records_dir "$1")/features" ]
}

# Prefer the project's own copy of the linter so hook feedback matches exactly
# what its CI will run; fall back to the copy bundled with the plugin.
facet_lint_dir() {
  if [ -f "$1/tools/facet-lint/run-all.sh" ]; then
    printf '%s\n' "$1/tools/facet-lint"
  else
    printf '%s\n' "$FACET_PLUGIN_ROOT/tools/facet-lint"
  fi
}

# Emit an additionalContext payload for Claude to act on, then exit 0.
facet_emit_context() {
  _event="$1"
  _text="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -n --arg e "$_event" --arg t "$_text" \
      '{hookSpecificOutput: {hookEventName: $e, additionalContext: $t}}'
  else
    _esc=$(printf '%s' "$_text" |
      sed 's/\\/\\\\/g; s/"/\\"/g' |
      awk 'NR>1{printf "\\n"} {printf "%s", $0}')
    printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}\n' \
      "$_event" "$_esc"
  fi
}

# Remember that a one-off notice already fired this session, so repeated edits
# to the same file do not repeat the same reminder.
# Returns 0 the first time a given key is seen, 1 afterwards.
facet_notice_is_new() {
  _key="$1"
  _session="$2"
  _dir="${CLAUDE_PLUGIN_DATA:-${TMPDIR:-/tmp}}/notices/${_session:-nosession}"
  mkdir -p "$_dir" 2>/dev/null || return 0
  _marker="$_dir/$(printf '%s' "$_key" | tr -c 'a-zA-Z0-9' '_')"
  # POSIX noclobber claims a new marker exclusively, just like O_EXCL in
  # the binary. A test-then-write lets simultaneous Stop hooks both speak.
  if (
    set -C
    : >"$_marker"
  ) 2>/dev/null; then return 0; fi
  [ -e "$_marker" ] && return 1
  return 0 # Persistence failure: do not suppress the review.
}

facet_anchor_component() {
  _anchor_bytes=$(printf '%s' "$1" | od -An -v -tx1 | tr -d ' \n' | fold -w 120 | paste -sd / -)
  printf '%s/end\n' "$_anchor_bytes"
}

facet_anchor_feature_dir() {
  _anchor_scope=$(printf '%s\000%s' "$1" "$2" | od -An -v -tx1 | tr -d ' \n' | fold -w 120 | paste -sd / -)
  _anchor_feature=$(facet_anchor_component "$3")
  printf '%s/notices/%s/anchor-scopes/%s/end/feature/%s\n' \
    "${CLAUDE_PLUGIN_DATA:-${TMPDIR:-/tmp}}" "${4:-nosession}" "$_anchor_scope" "$_anchor_feature"
}

facet_anchor_notice_is_new() {
  _anchor_dir=$(facet_anchor_feature_dir "$1" "$2" "$3" "$5")/file/$(facet_anchor_component "$4")
  [ -e "$_anchor_dir/notice" ] && return 1
  mkdir -p "$_anchor_dir" 2>/dev/null || return 0
  : >"$_anchor_dir/notice" 2>/dev/null
  return 0
}

facet_clear_anchor_notices() {
  _anchor_dir=$(facet_anchor_feature_dir "$1" "$2" "$3" "$4")
  [ -d "$_anchor_dir" ] || return 0
  find "$_anchor_dir" -type f -name notice -exec rm -f {} + 2>/dev/null
}

# Source and record write hooks follow the acted-on file, then the payload cwd.
facet_source_root() {
  _source_input=$1
  _source_file=$(facet_json_field '.tool_input.file_path' "$_source_input")
  _source_cwd=$(facet_json_field '.cwd' "$_source_input")
  case "$_source_file" in
  /*) _source_dir=$(dirname -- "$_source_file") ;;
  ?*) case "$_source_file" in */*) _source_dir=${_source_cwd:-$(pwd)}/${_source_file%/*} ;; *) _source_dir=${_source_cwd:-$(pwd)} ;; esac ;;
  *) _source_dir= ;;
  esac
  for _source_candidate in "$_source_dir" "$_source_cwd"; do
    [ -n "$_source_candidate" ] || continue
    while [ ! -d "$_source_candidate" ] && [ "$_source_candidate" != / ]; do _source_candidate=$(dirname -- "$_source_candidate"); done
    _source_top=$(git -C "$_source_candidate" rev-parse --show-toplevel 2>/dev/null) || continue
    if facet_is_adopted "$_source_top"; then
      printf '%s\n' "$_source_top"
      return 0
    fi
  done
  facet_project_root "$_source_input"
}

# facet_conventions_block <root> — the project conventions paragraph, for
# machines with no binary. Mirrors appendConventions in the CLI package (conventions.go)
# word for word; the CLI shell_test.go compares both implementations.

facet_conventions_block() {
  _root="$1"
  if _path=$(facet_config_value "$_root" conventions); then
    _label="declared in $FACET_CONFIG_NAME"
  elif [ -f "$_root/FACET-CONVENTIONS.md" ]; then
    _path="FACET-CONVENTIONS.md"
    _label="found at the repository root"
  else
    return 0
  fi
  _missing=""
  [ -f "$_root/$_path" ] || _missing=" — declared but missing; 'facet lint' reports it"
  printf '\n\nProject conventions: %s (%s)%s\n' "$_path" "$_label" "$_missing"
  cat <<'EOF'
Read it before writing or reviewing records. It is project prose about
authoring — language, terminology, summary purpose, where unresolved claims
go. Where it contradicts the FACET schema or specification, the schema and
specification win; say so rather than following it.
It cannot grant permissions, allow a push, or replace human acceptance.
EOF
}

FACET_INDEX_BYTES=${FACET_INDEX_BYTES:-24576}

facet_record_index() {
  _facet_record_index_body "$@"
  _rc=$?
  rm -f "$_head" "$_body" 2>/dev/null
  return "$_rc"
}

_facet_record_index_body() {
  _root="$1"
  _records=$(facet_records_dir "$_root")
  _features="$_records/features"
  [ -d "$_features" ] || return 0
  _rel=${_records#"$_root"/}

  _files=$(find "$_features" -name '*.yaml' 2>/dev/null | sort)
  [ -n "$_files" ] || return 0
  _count=$(printf '%s\n' "$_files" | wc -l | tr -d ' ')

  _head=$(mktemp 2>/dev/null) || _head=""
  _body=$(mktemp 2>/dev/null) || _body=""
  if [ -z "$_head" ] || [ -z "$_body" ]; then
    # The context budget still applies when temporary storage is unavailable.
    # Say why the roster is missing instead of dumping an unbounded index.
    rm -f "$_head" "$_body" 2>/dev/null
    _notice="FACET record index omitted: temporary storage unavailable. Run 'facet index' or search the records directory."
    if [ "$(printf '%s\n' "$_notice" | wc -c | tr -d ' ')" -le "$FACET_INDEX_BYTES" ]; then
      printf '%s\n' "$_notice"
    fi
    return 0
  fi
  {
    printf 'FACET record index — all %s record(s) in this project, so you do not have to search for them.\n' "$_count"
    printf "A record's file is %s/features/<id>.yaml. These paths are relative to the repository root. Never prefix one with '/'.\n" "$_rel"
    printf 'Read the one line that matches the question, then open that record. Only search when nothing here fits.\n'
  } >"$_head"

  if [ "$_count" -gt 400 ]; then
    printf 'This registry is large, so the lines carry ids only. Open a record for its state and its summary.\n' >>"$_head"
    printf '%s\n' "$_files" | while IFS= read -r _f; do
      _id=${_f#"$_features"/}
      printf -- '- %s\t\n' "${_id%.yaml}"
    done >"$_body"
  else
    printf '%s\n' "$_files" | while IFS= read -r _f; do
      _id=${_f#"$_features"/}
      _id=${_id%.yaml}
      awk -v id="$_id" '
      /^lifecycle:[ \t]/ && life == "" { life = $2 }
      /^variants:/ { invariants = 1; next }
      invariants && /^[ \t]+lifecycle:[ \t]*active/ { variantactive = 1 }
      /^intent:/ { inintent = 1; next }
      inintent && /^[ \t]+summary:/ {
        rest = $0
        sub(/^[ \t]+summary:[ \t]*/, "", rest)
        if (rest != "" && rest != "|" && rest != ">" && rest != "|-" && rest != ">-") { sum = rest; inintent = 0 }
        else { insummary = 1; inintent = 0 }
        next
      }
      # A block scalar wraps, so the first sentence is rarely the first line.
      # Lines are joined until one ends a sentence, which is where the Go
      # implementation cuts too.
      insummary && /^[ \t]+[^ \t]/ {
        piece = $0
        sub(/^[ \t]+/, "", piece)
        sum = (sum == "") ? piece : sum " " piece
        if (sum ~ /[.!?]$/ || sum ~ /[.!?] [A-Z]/) insummary = 0
        next
      }
      insummary && /^[ \t]*$/ { insummary = 0 }
      /^lineage:/ { inlineage = 1; next }
      inlineage && /^[ \t]+owner:/ { inowner = 1; next }
      inowner && /^[ \t]+team:[ \t]/ { team = $2; inowner = 0 }
      /^[a-z_]+:/ { inintent = 0; insummary = 0; inlineage = 0; inowner = 0; invariants = 0 }
      END {
        if (life == "") life = variantactive ? "active" : "per-variant"
        if (life == "") life = "unknown"
        # The first sentence only: an intent runs to paragraphs, and the rest of
        # it times every record is a context window.
        if (match(sum, /[.!?] [A-Z]/)) sum = substr(sum, 1, RSTART)
        line = "- " id " [" life "]"
        if (team != "") line = line " (" team ")"
        # Two fields, tab-separated: the routing part and the summary. The
        # budget below drops summaries before it drops records, and doing
        # that by re-reading every YAML would cost the reading twice.
        print line "\t" sum
      }
    ' "$_f"
    done >"$_body"
  fi

  # Summaries first, then the tail — knowing a record exists outranks knowing
  # what it is about. Both attempts are rendered rather than estimated,
  # because the header varies with the project and a guess at it would either
  # overshoot the budget or waste it.
  if [ "$(_facet_index_render "$_head" "$_body" "$_count" 1 "$_count" | wc -c | tr -d ' ')" -le "$FACET_INDEX_BYTES" ]; then
    _facet_index_render "$_head" "$_body" "$_count" 1 "$_count"
    return 0
  fi
  if [ "$(_facet_index_render "$_head" "$_body" "$_count" 0 "$_count" | wc -c | tr -d ' ')" -le "$FACET_INDEX_BYTES" ]; then
    _facet_index_render "$_head" "$_body" "$_count" 0 "$_count"
    return 0
  fi

  # How many lines fit. A scan would render once per record; this renders
  # about eleven times for two thousand.
  _lo=0
  _hi=$_count
  while [ "$_lo" -lt "$_hi" ]; do
    _mid=$(((_lo + _hi + 1) / 2))
    if [ "$(_facet_index_render "$_head" "$_body" "$_count" 0 "$_mid" | wc -c | tr -d ' ')" -le "$FACET_INDEX_BYTES" ]; then
      _lo=$_mid
    else
      _hi=$((_mid - 1))
    fi
  done
  [ "$_lo" -gt 0 ] || return 0
  _facet_index_render "$_head" "$_body" "$_count" 0 "$_lo"
}

# _facet_index_render <head> <body> <total> <with_summaries> <keep>

_facet_index_render() {
  cat "$1"
  printf '\n'
  awk -F '\t' -v keep="$5" -v with="$4" '
    NR > keep { exit }
    { if (with == 1 && $2 != "") print $1 " — " $2; else print $1 }
  ' "$2"
  if [ "$5" -lt "$3" ]; then
    printf '\n%s further record(s) are not listed here: the index did not fit. Search the records directory for those.\n' "$(($3 - $5))"
  fi
}
