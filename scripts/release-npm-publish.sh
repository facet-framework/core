#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: related
#
# Publishes the tarballs of a publish plan in the plan's order, and can be run
# again after a run that stopped half way.

set -eu

die() {
  echo "release-npm-publish: $*" >&2
  exit 2
}

[ "$#" -eq 1 ] || die "usage: release-npm-publish.sh <dist>"
PLAN="$1/npm/publish-plan.json"
[ -f "$PLAN" ] || die "$PLAN is missing"
command -v jq >/dev/null 2>&1 || die "jq is required"
command -v npm >/dev/null 2>&1 || die "npm is required"

jq -e '.publishable == true and .complete == true and (.packages | length > 0)' "$PLAN" >/dev/null ||
  die "the plan is not publishable: $(jq -c '{publishable, complete, blocked_by}' "$PLAN")"

npm_version=$(npm --version)
# Trusted publishing came with npm 11.5.1.
printf '%s\n' "$npm_version" | awk -F. '{ exit !($1 > 11 || ($1 == 11 && ($2 > 5 || ($2 == 5 && $3 >= 1)))) }' ||
  die "npm $npm_version cannot publish through trusted publishing; 11.5.1 or later is needed"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
ERR="$TMP/err"
TAB=$(printf '\t')
jq -r '.packages | sort_by(.order)[] | [.name, .version, .tarball, .integrity, .dist_tag, .access] | @tsv' "$PLAN" >"$TMP/list"

# Which package names does the registry not know yet?
new=""
while IFS=$TAB read -r name version tarball integrity tag access; do
  if npm view "$name" name >/dev/null 2>"$ERR"; then
    continue
  elif grep -q 'E404' "$ERR"; then
    new="$new $name"
  else
    die "cannot ask the registry for $name: $(cat "$ERR")"
  fi
done <"$TMP/list"
# actions/setup-node writes a placeholder into NODE_AUTH_TOKEN when no token
# is supplied; it authenticates nothing and counts as no token.
token=${NODE_AUTH_TOKEN:-}
[ "$token" != "XXXXX-XXXXX-XXXXX-XXXXX" ] || token=""
if [ -n "$new" ]; then
  [ -n "$token" ] ||
    die "these packages are not on the registry yet:$new. A first publication needs a token (NODE_AUTH_TOKEN); trusted publishing can only be configured for a package that exists"
  echo "release-npm-publish: first publication of:$new (with the token)"
fi

while IFS=$TAB read -r name version tarball integrity tag access; do
  [ -f "$1/npm/$tarball" ] || die "$name@$version: $1/npm/$tarball is missing"
  if found=$(npm view "$name@$version" dist.integrity 2>"$ERR"); then
    if [ "$found" = "$integrity" ]; then
      echo "release-npm-publish: $name@$version is already published with this tarball; skipped"
      continue
    fi
    [ -n "$found" ] || die "the registry answered nothing for $name@$version; refusing to guess"
    die "$name@$version is published with integrity $found, not the plan's $integrity"
  elif ! grep -q 'E404' "$ERR"; then
    die "cannot ask the registry for $name@$version: $(cat "$ERR")"
  fi
  npm publish "$1/npm/$tarball" --tag "$tag" --access "$access" --provenance
done <"$TMP/list"
echo "release-npm-publish: every package of the plan is on the registry"
