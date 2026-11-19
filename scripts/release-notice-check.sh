#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: related
#
# Fails unless every file or directory NOTICE refers to exists beside it.

set -eu

ROOT=${1:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}
[ -s "$ROOT/NOTICE" ] || {
  echo "release-notice-check: $ROOT/NOTICE is missing or empty" >&2
  exit 2
}
missing=0
for name in $(grep -oE '(^|[^A-Za-z0-9_])[A-Z][A-Z0-9_]+(\.[a-z]+|/)' "$ROOT/NOTICE" | sed 's/^[^A-Z]//' | sort -u); do
  case "$name" in
  */) [ -d "$ROOT/$name" ] && [ -n "$(ls -A "$ROOT/$name")" ] ;;
  *) [ -s "$ROOT/$name" ] ;;
  esac || {
    echo "release-notice-check: NOTICE refers to $name, which is missing or empty" >&2
    missing=1
  }
done
[ "$missing" -eq 0 ] || exit 1
echo "release-notice-check: every file NOTICE refers to is present"
