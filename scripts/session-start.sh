#!/usr/bin/env sh
# @facet: authoring/session/registry-context
# @facet-role: primary

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(cat)

# Prefer the binary when one exists; fall through to the shell logic below.
facet_delegate_to_binary "session-start" "$INPUT" && exit 0
# Codex restores context immediately after compaction through SessionStart.
# Reuse the existing rule builder and change only its envelope event.
if [ "$(facet_json_field '.source' "$INPUT")" = compact ]; then
  printf '%s' "$INPUT" | sh "$SCRIPT_DIR/pre-compact.sh" | sed 's/"hookEventName": "PreCompact"/"hookEventName": "SessionStart"/; s/"hookEventName":"PreCompact"/"hookEventName":"SessionStart"/'
  exit 0
fi
ROOT=$(facet_project_root "$INPUT")

# A monorepo declaring several registries has no single records directory, and
# this shell fallback reads only the flat top-level keys of the config. Saying
# so beats reporting the default layout, which in such a project is nobody's.
if facet_declares_registries "$ROOT"; then
  facet_emit_context "SessionStart" "This project documents its features with FACET and declares several
registries in .facetrc.yaml. No facet binary is available in this session, and
this shell fallback cannot read a registries block, so no record counts follow.

Read .facetrc.yaml for the layout. Each registry has its own records directory
and its own source directories, and a record belongs to exactly one of them.
Ids are global: the same id in two registries is the SAME feature, documented
independently by each product, so reuse it rather than inventing a suffixed one.

Working rules:
- A change to user-visible behaviour updates the matching record in the same
  change, not in a follow-up (FACET spec 12.1).
- 'lifecycle: active' requires at least one '@facet:' anchor in source.
- Ground all five Knowledge slots in implementation, contract, documentation
  and real entry points. Draft supported answers; identify inferred answers in
  the handover for human review. Leave an answer absent only when evidence
  cannot answer it, and flag the missing owner input outside the YAML.
  Never invent owner facts, permissions, entry points or usage history.

Run 'facet lint' for the counts and the findings.
Run 'facet conventions' for each registry's authoring conventions document."
  exit 0
fi

facet_is_adopted "$ROOT" || exit 0

RECORDS_DIR=$(facet_records_dir "$ROOT")
FEATURES_DIR="$RECORDS_DIR/features"
REL_RECORDS=${RECORDS_DIR#"$ROOT"/}

count_lifecycle() {
  find "$FEATURES_DIR" -name '*.yaml' 2>/dev/null |
    while IFS= read -r f; do
      grep -h "^lifecycle:[[:space:]]*$1" "$f" 2>/dev/null
    done |
    wc -l | tr -d ' '
}

TOTAL=$(find "$FEATURES_DIR" -name '*.yaml' 2>/dev/null | wc -l | tr -d ' ')
[ "$TOTAL" -eq 0 ] && exit 0

ACTIVE=$(count_lifecycle active)
PLANNED=$(count_lifecycle planned)
DEPRECATED=$(count_lifecycle deprecated)

SPEC_VERSION=$(find "$FEATURES_DIR" -name '*.yaml' 2>/dev/null |
  head -n 1 |
  while IFS= read -r f; do
    grep -h '^schema_version:' "$f" 2>/dev/null | head -n 1 |
      sed 's/^schema_version:[[:space:]]*//; s/"//g'
  done)

VARIANT_MAP="$RECORDS_DIR/variants.yaml"
if [ -f "$VARIANT_MAP" ]; then
  VARIANT_NAMES=$(sed -n '/^variants:/,/^[a-z]/p' "$VARIANT_MAP" |
    grep -E '^  [a-z][a-z0-9-]*:' | sed 's/^  //; s/:.*//' | tr '\n' ' ')
  VARIANT_BLOCK="
Variants: $VARIANT_NAMES(declared in ${VARIANT_MAP#"$ROOT"/}).
- A feature existing on several variants is ONE record with a per-variant
  lifecycle. Never split a record on a variant boundary.
- A record with variants has no top-level 'lifecycle'; the two are exclusive.
- 'active' on a variant needs an anchor in a path exclusive to it. Shared code
  is worth anchoring but proves no variant.
"
fi

# The project's conventions document, if it keeps one. Empty otherwise, so
# the context of a project without one is exactly what it was before.
CONVENTIONS=$(facet_conventions_block "$ROOT")

INDEX=$(facet_record_index "$ROOT")
[ -n "$INDEX" ] && INDEX="

$INDEX"

# The heredoc lives in a function: bash 3.2 (macOS /bin/sh) misparses a
# heredoc with apostrophes inside $(...) and rejects the whole script.
session_context() {
  cat <<EOF
This project documents its features with FACET (Feature-Anchored Canonical
Executable Truth${SPEC_VERSION:+, schema $SPEC_VERSION}).

Registry: $TOTAL feature record(s) — $ACTIVE active, $PLANNED planned, $DEPRECATED deprecated.
Records live in $REL_RECORDS/features/<domain>/<capability>/<feature>.yaml.
Source files link back to them with '@facet: <id>' comments.
${VARIANT_BLOCK}

Working rules:
- A change to user-visible behaviour updates the matching record in the same
  change, not in a follow-up (FACET spec 12.1).
- A record's 'lifecycle: active' requires at least one '@facet:' anchor in
  source. Use 'lifecycle: planned' for records whose code does not exist yet.
- Ground all five Knowledge slots in implementation, contract, documentation
  and real entry points. Draft supported answers; identify inferred answers in
  the handover for human review. Leave an answer absent only when evidence
  cannot answer it, and flag the missing owner input outside the YAML.
  Never invent owner facts, permissions, entry points or usage history.

Available skills: authoring (write records), anchoring (place tags),
lint (verify), knowledge (surface open questions). Select them through your
host skill picker.${CONVENTIONS:+

$CONVENTIONS}
${INDEX}
EOF
}

facet_emit_context "SessionStart" "$(session_context)"
