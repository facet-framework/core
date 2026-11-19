#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: related
#
# The release notes of one Core version, read from the changelog humans wrote.
# Nothing is generated from commits: the notes are the changelog section, or
# the run stops.

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

die() {
  echo "release-changelog: $*" >&2
  exit 2
}

[ "${1:-}" = notes ] || die "usage: release-changelog.sh notes <version> [<changelog>]"
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || die "usage: release-changelog.sh notes <version> [<changelog>]"
VERSION=$2
CHANGELOG=${3:-$ROOT/CHANGELOG.md}
[ -f "$CHANGELOG" ] || die "$CHANGELOG does not exist"
KIND=$(sh "$ROOT/scripts/release-version.sh" shape "$VERSION") || exit 2
[ "$KIND" != dev ] || die "$VERSION is a development version; it is never released with notes"

# The line number of the level-two heading whose title (the text after "## "
# up to an optional " — "/" - " date part) is exactly $1, bracketed or not.
# Two such headings are refused: which one holds the notes would be a guess.
heading_line() {
  lines=$(awk -v want="$1" '
    /^## / {
      title = substr($0, 4)
      sub(/[[:space:]]+(—|-)[[:space:]].*$/, "", title)
      sub(/[[:space:]]+$/, "", title)
      if (title == want || title == "[" want "]") print NR
    }' "$CHANGELOG")
  [ "$(printf '%s' "$lines" | grep -c .)" -le 1 ] ||
    die "$CHANGELOG has more than one $1 section (lines $(printf '%s' "$lines" | tr '\n' ' ' | sed 's/ $//'))"
  printf '%s' "$lines"
}

# The section body after line $1, up to the next level-two heading.
section_after() {
  awk -v from="$1" 'NR > from && /^## / { exit } NR > from { print }' "$CHANGELOG"
}

start=$(heading_line "$VERSION")
if [ -n "$start" ]; then
  heading=$(sed -n "${start}p" "$CHANGELOG")
  if [ "$KIND" = release ]; then
    case "$heading" in
    *[Uu]nreleased*) die "the $VERSION section is still marked unreleased: $heading" ;;
    *[0-9][0-9][0-9][0-9]-[0-1][0-9]-[0-3][0-9]*) ;;
    *) die "the $VERSION section is not dated: $heading" ;;
    esac
  fi
elif [ "$KIND" = rc ]; then
  start=$(heading_line Unreleased)
  [ -n "$start" ] || die "$CHANGELOG has neither a $VERSION section nor an Unreleased section"
else
  die "$CHANGELOG has no $VERSION section; a release needs its own dated section"
fi

body=$(section_after "$start")
printf '%s\n' "$body" | grep -q '^[[:space:]]*[-*] ' ||
  die "the section at line $start of $CHANGELOG lists no change; empty notes are not released"
# Leading and trailing blank lines dropped, the rest as written.
printf '%s\n' "$body" | awk 'NF { started = 1 } started { lines[++n] = $0 } END { while (n > 0 && lines[n] ~ /^[[:space:]]*$/) n--; for (i = 1; i <= n; i++) print lines[i] }'
