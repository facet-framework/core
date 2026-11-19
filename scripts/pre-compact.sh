#!/usr/bin/env sh
# @facet: authoring/session/registry-context
# @facet-role: primary
# PreCompact hook — restates the working rules just before the context is
# compacted away.

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(cat)

# Prefer the binary when one exists; fall through to the shell logic below.
facet_delegate_to_binary "pre-compact" "$INPUT" && exit 0
ROOT=$(facet_project_root "$INPUT")

# See session-start.sh: this fallback cannot read a registries block, so it
# carries the rules forward without pretending to know the layout.
if facet_declares_registries "$ROOT"; then
  facet_emit_context "PreCompact" "Carrying forward: this project documents its features with FACET, across
several registries declared in .facetrc.yaml. Ids are global — the same id in
two of them is the SAME feature, with one record per registry.

- A change to user-visible behaviour updates the matching record in the same
  change, not in a follow-up (spec 12.1).
- 'lifecycle: active' requires an '@facet:' anchor in source.
- Ground all five Knowledge slots in implementation, contract, documentation
  and real entry points. Draft supported answers; identify inferred answers in
  the handover for human review. Leave an answer absent only when evidence
  cannot answer it, and flag the missing owner input outside the YAML.
  Never invent owner facts, permissions, entry points or usage history.
- Run 'facet lint' before committing; the commit gate runs it anyway.
- Run 'facet conventions' for each registry's authoring conventions document."
  exit 0
fi

facet_is_adopted "$ROOT" || exit 0

RECORDS_DIR=$(facet_records_dir "$ROOT")
REL_RECORDS=${RECORDS_DIR#"$ROOT"/}

TOUCHED=""
if git -C "$ROOT" rev-parse --git-dir >/dev/null 2>&1; then
  TOUCHED=$(git -C "$ROOT" status --porcelain -- "$REL_RECORDS/features" 2>/dev/null |
    sed 's/^...//; s/^.* -> //' |
    grep "^$REL_RECORDS/features/.*\.yaml$" |
    sed "s|^$REL_RECORDS/features/||; s|\.yaml$||" |
    sort -u)
fi

MESSAGE="Carrying forward: this project documents its features with FACET.

- A change to user-visible behaviour updates the matching record in the same
  change, not in a follow-up (spec 12.1).
- 'lifecycle: active' requires an '@facet:' anchor in source.
- Ground all five Knowledge slots in implementation, contract, documentation
  and real entry points. Draft supported answers; identify inferred answers in
  the handover for human review. Leave an answer absent only when evidence
  cannot answer it, and flag the missing owner input outside the YAML.
  Never invent owner facts, permissions, entry points or usage history.
- Run 'facet lint' before committing; the commit gate runs it anyway."

if [ -n "$TOUCHED" ]; then
  MESSAGE="$MESSAGE

Records changed so far in this working tree:
$(printf '%s\n' "$TOUCHED" | sed 's/^/- /')
Their contract scenarios are the reference for what the code must do."
fi

VARIANT_MAP="$RECORDS_DIR/variants.yaml"
if [ -f "$VARIANT_MAP" ]; then
  VARIANT_NAMES=$(sed -n '/^variants:/,/^[a-z]/p' "$VARIANT_MAP" |
    grep -E '^  [a-z][a-z0-9-]*:' | sed 's/^  //; s/:.*//' | tr '\n' ' ')
  MESSAGE="$MESSAGE

Variants: $VARIANT_NAMES(declared in ${VARIANT_MAP#"$ROOT"/}).
- A feature existing on several variants is ONE record with a per-variant
  lifecycle. Never split a record on a variant boundary.
- A record with variants has no top-level 'lifecycle'; the two are exclusive.
- 'active' on a variant needs an anchor in a path exclusive to it. Shared code
  is worth anchoring but proves no variant."
fi

# The conventions document, said again for the same reason as the rules: it is
# among the first things a compaction summarises away.
CONVENTIONS=$(facet_conventions_block "$ROOT")
if [ -n "$CONVENTIONS" ]; then
  MESSAGE="$MESSAGE

$CONVENTIONS"
fi

# Restated in full rather than "as before": compaction is the moment the
# roster is summarised away, and an agent that half-remembers which records
# exist searches for the ones it forgot.
INDEX=$(facet_record_index "$ROOT")
if [ -n "$INDEX" ]; then
  MESSAGE="$MESSAGE

$INDEX"
fi

facet_emit_context "PreCompact" "$MESSAGE"
