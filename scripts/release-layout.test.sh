#!/usr/bin/env sh
# Exercise receipt-based public Core layout detection and the alternate layout.
# Each case checks the layout and tag prefix as well as rejection status.
# Explicit public selection still requires a valid Core receipt.
# @facet-ignore-file
set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
FAILED=0
fail() {
  printf 'FAIL %s\n  %s\n' "$1" "$2"
  FAILED=1
}
pass() { printf 'ok   %s\n' "$1"; }

# decide <tree> [layout]: "<status> <LAYOUT> <CORE_TAG_PREFIX>" plus stderr in $WORK/err.
decide() {
  (
    if [ -n "${2:-}" ]; then FACET_RELEASE_LAYOUT=$2; else unset FACET_RELEASE_LAYOUT; fi
    LAYOUT=unset CORE_TAG_PREFIX=unset
    . "$ROOT/scripts/release-layout.sh"
    release_layout "$1" 2>"$WORK/err"
    printf '%s %s %s\n' "$?" "$LAYOUT" "$CORE_TAG_PREFIX"
  )
}

mkdir -p "$WORK/dev/cmd/facet-core" "$WORK/dev/facet-framework/core" \
  "$WORK/public/cmd/facet" "$WORK/bare/cmd/facet"
printf '{"schema_version": 1, "product": "core", "version": "0.1.0", "files": []}\n' >"$WORK/public/PUBLIC-SOURCE-MANIFEST.json"

got=$(decide "$WORK/dev")
[ "$got" = "0 development internal/core/" ] && pass "the complete alternate layout is detected" ||
  fail "development tree" "$got $(cat "$WORK/err")"

got=$(decide "$WORK/public")
[ "$got" = "0 public v" ] && pass "a Core receipt is the public layout with v tags" ||
  fail "public tree" "$got $(cat "$WORK/err")"

# A public tree that lost its receipt: no fallback to development tag names.
got=$(decide "$WORK/bare")
if [ "$got" = "2 unset unset" ] && grep -q 'cannot tell the layout' "$WORK/err"; then
  pass "a tree with neither a receipt nor the development marks stops, setting nothing"
else
  fail "a tree without marks stops" "$got $(cat "$WORK/err")"
fi

mkdir -p "$WORK/half/cmd/facet-core"
got=$(decide "$WORK/half")
[ "${got%% *}" = 2 ] && pass "an incomplete alternate layout is refused" ||
  fail "half a development tree stops" "$got"

got=$(decide "$WORK/public" development)
[ "$got" = "0 development internal/core/" ] && pass "FACET_RELEASE_LAYOUT=development is taken as named, even beside a receipt" ||
  fail "a pinned development layout" "$got $(cat "$WORK/err")"

got=$(decide "$WORK/bare" public)
[ "${got%% *}" = 2 ] && grep -q 'needs PUBLIC-SOURCE-MANIFEST.json' "$WORK/err" &&
  pass "FACET_RELEASE_LAYOUT=public is refused without a receipt" ||
  fail "a pinned public layout needs its receipt" "$got $(cat "$WORK/err")"

got=$(decide "$WORK/dev" sideways)
[ "${got%% *}" = 2 ] && grep -q "is not development or public" "$WORK/err" &&
  pass "an unknown FACET_RELEASE_LAYOUT is refused" ||
  fail "unknown layout" "$got $(cat "$WORK/err")"

# The tree this suite runs in is one of the two layouts. Which one is read from
# the mark each layout carries, so the same suite holds in both.
if [ -f "$ROOT/PUBLIC-SOURCE-MANIFEST.json" ]; then
  want="0 public v"
else
  want="0 development internal/core/"
fi
got=$(decide "$ROOT")
[ "$got" = "$want" ] && pass "this checkout is detected as the layout its marks name (${want#0 })" ||
  fail "this checkout" "wanted '$want', got '$got' $(cat "$WORK/err")"

exit "$FAILED"
